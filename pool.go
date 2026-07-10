package mygram

import (
	"context"
	"sync/atomic"
	"time"
)

// Default pool sizing and retry values.
const (
	defaultMaxOpenConns = 10
	defaultMaxRetries   = 1
	defaultBaseBackoff  = 50 * time.Millisecond

	// minReapInterval floors how often the background reaper runs, so a small
	// ConnMaxLifetime/ConnMaxIdleTime cannot turn it into a busy loop. Expiry is
	// still exact on borrow; the reaper is a best-effort sweep of idle sockets.
	minReapInterval = 100 * time.Millisecond
)

// RetryPolicy controls how the pool retries failed commands. Only failures
// that are safe to retry are ever retried: a command whose write never reached
// the server (any command), or a read-phase failure of an idempotent command.
// Server errors, validation errors and timeouts are never retried.
type RetryPolicy struct {
	// MaxRetries is the maximum number of additional attempts after the first.
	// The zero value selects the default of 1 (so up to two total attempts). A
	// negative value explicitly disables retries (zero additional attempts);
	// use it when a workload must never re-issue a command, even a safe one.
	MaxRetries int

	// BaseBackoff is the base delay for the exponential-with-full-jitter
	// backoff between attempts. Default is 50ms.
	BaseBackoff time.Duration
}

// PoolConfig configures a Pool. The embedded ClientConfig is applied to every
// connection the pool opens. Time-based limits use time.Duration (the pool is
// Go-native), whereas the embedded ClientConfig keeps its millisecond fields
// for parity with the other language clients.
type PoolConfig struct {
	ClientConfig

	// MaxOpenConns bounds the total number of connections (idle plus in use).
	// A borrow that would exceed it blocks until a connection is returned or
	// the context is canceled. 0 or negative means the default (10). There is
	// deliberately no "unlimited" option: an explicit cap is the point.
	MaxOpenConns int

	// MaxIdleConns bounds how many idle connections are retained for reuse.
	// 0 or negative means "same as MaxOpenConns". Values above MaxOpenConns are
	// clamped to MaxOpenConns.
	MaxIdleConns int

	// ConnMaxLifetime is the maximum age of a connection measured from when it
	// was opened. Expired connections are discarded on borrow, and a background
	// reaper also sweeps idle ones so a quiet pool does not keep them open. 0
	// means no limit.
	ConnMaxLifetime time.Duration

	// ConnMaxIdleTime is the maximum time a connection may sit idle before it
	// is discarded. It is enforced both on borrow and by the background reaper.
	// 0 means no limit.
	ConnMaxIdleTime time.Duration

	// Retry is the retry policy for idempotent and never-sent commands. A zero
	// value selects the default policy.
	Retry RetryPolicy

	// CircuitBreaker configures a circuit breaker around the command API. It
	// sits outside the retry policy, so an open breaker fails fast without
	// retrying or touching the network. A nil pointer disables it.
	CircuitBreaker *CircuitBreakerConfig

	// OnEvent, when set, receives observability events (see PoolEvent). It is
	// called synchronously on the pool's hot path, so it must not block; a
	// panic in the callback is recovered and ignored.
	OnEvent func(event PoolEvent, payload map[string]any)
}

// pooledConn is a connection tracked by the pool together with its timestamps.
type pooledConn struct {
	client    *Client
	createdAt time.Time
	idleSince time.Time
}

// Pool is a bounded pool of MygramDB connections. It exposes the same command
// methods as Client; for each call it borrows a connection, runs the request,
// and returns the connection to the pool, transparently reconnecting and
// retrying where it is safe to do so. A Pool is safe for concurrent use.
//
// The token accounting maintains the invariant that the number of open
// connections (idle plus in use) never exceeds MaxOpenConns: every connection
// holds exactly one slot in sem from the moment it is dialed until it is
// closed. Idle connections keep their slot while parked in the idle channel.
type Pool struct {
	cfg PoolConfig

	// sem caps the number of open connections. A value in the channel
	// represents one open connection (idle or in use).
	sem chan struct{}

	// idle holds connections available for reuse. Its capacity bounds the
	// number of retained idle connections.
	idle chan *pooledConn

	closed atomic.Bool

	// stop signals the background reaper to exit. It is closed exactly once, by
	// Close. When no time-based limit is configured no reaper runs, but the
	// channel is still created and closed so Close stays uniform.
	stop chan struct{}

	// breaker is the optional circuit breaker guarding the command API. nil
	// when PoolConfig.CircuitBreaker was not set.
	breaker *circuitBreaker

	// Statistics counters (nanoseconds for the duration).
	waitCount      atomic.Int64
	waitDurationNs atomic.Int64
	discarded      atomic.Int64
	retries        atomic.Int64
}

// PoolStats is a snapshot of a Pool's runtime statistics.
type PoolStats struct {
	// Open is the number of open connections (idle plus in use).
	Open int
	// Idle is the number of connections currently available for reuse.
	Idle int
	// InUse is the number of connections currently borrowed.
	InUse int
	// WaitCount is the approximate number of borrows that had to wait for a
	// connection. It counts entries into the waiting path, some of which may
	// then complete without actually blocking, so it slightly overstates
	// contention.
	WaitCount int64
	// WaitDuration is the cumulative time borrows spent in the waiting path.
	WaitDuration time.Duration
	// Discarded is the number of connections closed instead of being reused
	// (unhealthy, expired, or beyond the idle limit).
	Discarded int64
	// Retries is the number of request retries performed.
	Retries int64
}

// NewPool creates a pool with the given configuration. It performs no I/O;
// connections are opened lazily on first use.
func NewPool(cfg PoolConfig) *Pool {
	if cfg.MaxOpenConns <= 0 {
		cfg.MaxOpenConns = defaultMaxOpenConns
	}
	if cfg.MaxIdleConns <= 0 || cfg.MaxIdleConns > cfg.MaxOpenConns {
		cfg.MaxIdleConns = cfg.MaxOpenConns
	}
	// A negative MaxRetries is an explicit opt-out and stays at zero. A zero
	// value with no backoff is the unset case and selects the default policy;
	// a zero value paired with a backoff is an explicit "no retries" too.
	switch {
	case cfg.Retry.MaxRetries < 0:
		cfg.Retry.MaxRetries = 0
	case cfg.Retry.MaxRetries == 0 && cfg.Retry.BaseBackoff == 0:
		cfg.Retry.MaxRetries = defaultMaxRetries
	}
	if cfg.Retry.BaseBackoff <= 0 {
		cfg.Retry.BaseBackoff = defaultBaseBackoff
	}
	p := &Pool{
		cfg:  cfg,
		sem:  make(chan struct{}, cfg.MaxOpenConns),
		idle: make(chan *pooledConn, cfg.MaxIdleConns),
		stop: make(chan struct{}),
	}
	if cfg.CircuitBreaker != nil {
		p.breaker = newCircuitBreaker(*cfg.CircuitBreaker, func(state string) {
			p.emit(EventBreakerStateChange, map[string]any{"state": state})
		})
	}
	if interval := reapInterval(cfg); interval > 0 {
		go p.reaper(interval)
	}
	return p
}

// reapInterval returns how often the background reaper should sweep idle
// connections, or 0 when no time-based limit is configured (so no reaper is
// started). It tracks the shorter of the two limits and is floored at
// minReapInterval.
func reapInterval(cfg PoolConfig) time.Duration {
	d := cfg.ConnMaxIdleTime
	if cfg.ConnMaxLifetime > 0 && (d <= 0 || cfg.ConnMaxLifetime < d) {
		d = cfg.ConnMaxLifetime
	}
	if d <= 0 {
		return 0
	}
	if d < minReapInterval {
		d = minReapInterval
	}
	return d
}

// reaper periodically discards idle connections that have exceeded their
// lifetime or idle limit, so a pool that goes quiet after a burst does not hold
// expired sockets (and their server-side connection slots) open until the next
// borrow. It exits when the pool is closed.
func (p *Pool) reaper(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.reapExpired()
		}
	}
}

// reapExpired sweeps the idle channel once, discarding expired (or, if the pool
// is closing, all) connections and re-parking the rest. It scans at most the
// number of connections present when it started, so a concurrently re-parked
// connection is never processed twice in one sweep.
func (p *Pool) reapExpired() {
	for n := len(p.idle); n > 0; n-- {
		select {
		case pc := <-p.idle:
			if p.closed.Load() || p.expired(pc) {
				p.discardParked(pc)
				continue
			}
			// parkIdle re-checks p.closed after the park, so a Close that drains
			// concurrently with this sweep cannot strand the connection in idle.
			p.parkIdle(pc)
		default:
			return
		}
	}
}

// Close closes the pool. Idle connections are closed immediately; connections
// currently in use are closed when they are returned. After Close, command
// methods return a ConnectionError. Close is idempotent.
func (p *Pool) Close() error {
	if p.closed.Swap(true) {
		return nil
	}
	close(p.stop)
	for {
		select {
		case pc := <-p.idle:
			_ = pc.client.Close()
			<-p.sem
		default:
			return nil
		}
	}
}

// Stats returns a snapshot of the pool's current statistics.
func (p *Pool) Stats() PoolStats {
	open := len(p.sem)
	idle := len(p.idle)
	inUse := open - idle
	if inUse < 0 {
		inUse = 0
	}
	return PoolStats{
		Open:         open,
		Idle:         idle,
		InUse:        inUse,
		WaitCount:    p.waitCount.Load(),
		WaitDuration: time.Duration(p.waitDurationNs.Load()),
		Discarded:    p.discarded.Load(),
		Retries:      p.retries.Load(),
	}
}

// WithConn borrows a single connection and runs fn against it. Use it to run a
// sequence of requests on one connection, or a request whose retry semantics
// you want to control yourself. The pool applies no retries around fn. The
// connection is returned to the pool afterwards, or discarded if fn reports a
// connection-level failure.
func (p *Pool) WithConn(ctx context.Context, fn func(c *Client) error) error {
	pc, err := p.acquire(ctx)
	if err != nil {
		return err
	}
	err = fn(pc.client)
	p.release(pc, err)
	return err
}

// run applies the circuit breaker (if configured) around runInner. The breaker
// sits outside retries, so an open breaker fails fast with a CircuitOpenError
// before any connection is borrowed.
func (p *Pool) run(ctx context.Context, idempotent bool, fn func(c *Client) error) error {
	if p.breaker == nil {
		return p.runInner(ctx, idempotent, fn)
	}
	if err := p.breaker.beforeRequest(); err != nil {
		return err
	}
	err := p.runInner(ctx, idempotent, fn)
	p.breaker.record(err)
	return err
}

// runInner borrows a connection, invokes fn, and returns the connection,
// retrying on safe failures according to the retry policy. A failure to obtain
// a connection (a dial failure) is itself retried when it is safe, so a briefly
// unavailable server is tolerated; a closed pool and context errors are not
// retried.
func (p *Pool) runInner(ctx context.Context, idempotent bool, fn func(c *Client) error) error {
	attempt := 0
	for {
		pc, err := p.acquire(ctx)
		if err != nil {
			if err == errPoolClosedSentinel || !shouldRetry(err, idempotent, attempt, p.cfg.Retry.MaxRetries) {
				return err
			}
			attempt++
			p.retries.Add(1)
			p.emit(EventRetry, map[string]any{"attempt": attempt, "error": err.Error()})
			if berr := p.sleepBackoff(ctx, attempt); berr != nil {
				return err
			}
			continue
		}
		err = fn(pc.client)
		p.release(pc, err)
		if err == nil {
			return nil
		}
		if !shouldRetry(err, idempotent, attempt, p.cfg.Retry.MaxRetries) {
			return err
		}
		attempt++
		p.retries.Add(1)
		p.emit(EventRetry, map[string]any{"attempt": attempt, "error": err.Error()})
		if berr := p.sleepBackoff(ctx, attempt); berr != nil {
			// Context expired during backoff; surface the operation error.
			return err
		}
	}
}

// acquire returns a connection ready for use, blocking until one is available
// or the context is canceled. On success the caller owns the connection and
// must return it via release.
func (p *Pool) acquire(ctx context.Context) (*pooledConn, error) {
	for {
		if p.closed.Load() {
			return nil, errPoolClosed()
		}

		// Fast path: an idle connection is immediately available.
		select {
		case pc := <-p.idle:
			if p.closed.Load() || p.expired(pc) {
				p.discardParked(pc)
				continue
			}
			p.emitAcquire(0)
			return pc, nil
		default:
		}

		// Fast path: an open slot is available, dial a new connection.
		select {
		case p.sem <- struct{}{}:
			pc, err := p.dialUnlessClosed(ctx)
			if err != nil {
				<-p.sem
				return nil, err
			}
			p.emitAcquire(0)
			return pc, nil
		default:
		}

		// Slow path: wait for an idle connection or a freed slot.
		start := time.Now()
		p.waitCount.Add(1)
		select {
		case pc := <-p.idle:
			waited := time.Since(start)
			p.waitDurationNs.Add(int64(waited))
			if p.closed.Load() || p.expired(pc) {
				p.discardParked(pc)
				continue
			}
			p.emitAcquire(waited)
			return pc, nil
		case p.sem <- struct{}{}:
			waited := time.Since(start)
			p.waitDurationNs.Add(int64(waited))
			pc, err := p.dialUnlessClosed(ctx)
			if err != nil {
				<-p.sem
				return nil, err
			}
			p.emitAcquire(waited)
			return pc, nil
		case <-ctx.Done():
			p.waitDurationNs.Add(int64(time.Since(start)))
			return nil, ctx.Err()
		}
	}
}

// emitAcquire reports a successful borrow, including how long it waited. It
// skips building the payload entirely when no callback is registered, keeping
// the hot path allocation-free in the common case.
func (p *Pool) emitAcquire(waited time.Duration) {
	if p.cfg.OnEvent == nil {
		return
	}
	p.emit(EventAcquire, map[string]any{"wait_seconds": waited.Seconds()})
}

// errPoolClosedSentinel is returned once the pool is closed. It is a single
// shared value so run can identify it by identity and never retry it.
var errPoolClosedSentinel = &ConnectionError{Message: "pool is closed", beforeSend: true}

// errPoolClosed returns the shared pool-closed error.
func errPoolClosed() error {
	return errPoolClosedSentinel
}

// dialUnlessClosed opens a connection under an already-reserved slot, unless
// the pool was closed after the slot was won (in which case the caller frees
// the slot).
func (p *Pool) dialUnlessClosed(ctx context.Context) (*pooledConn, error) {
	if p.closed.Load() {
		return nil, errPoolClosed()
	}
	return p.dial(ctx)
}

// release returns a connection to the pool, or discards it when it is no longer
// usable (connection-level failure, pool closed, expired, or the idle pool is
// full).
func (p *Pool) release(pc *pooledConn, err error) {
	if !connReusable(err) || p.closed.Load() || p.expired(pc) {
		p.discardParked(pc)
		return
	}
	pc.idleSince = time.Now()
	p.parkIdle(pc)
}

// parkIdle returns a healthy connection to the idle pool, discarding it if the
// idle pool is full or if a concurrent Close has already drained. Callers set
// idleSince as appropriate before parking (release refreshes it; the reaper
// preserves it).
func (p *Pool) parkIdle(pc *pooledConn) {
	select {
	case p.idle <- pc:
	default:
		p.discardParked(pc)
		return
	}
	// Guard against a Close that ran concurrently with the park above: if
	// Close's drain had already finished when we parked, this connection would
	// otherwise sit in idle forever, leaking its fd and sem slot. Reclaim one
	// idle connection to keep the count balanced — once closed, every idle
	// connection must be discarded, so any entry will do.
	if p.closed.Load() {
		select {
		case pc2 := <-p.idle:
			p.discardParked(pc2)
		default:
		}
	}
}

// discardParked closes a connection and frees its open slot.
func (p *Pool) discardParked(pc *pooledConn) {
	_ = pc.client.Close()
	<-p.sem
	p.discarded.Add(1)
	p.emit(EventConnectionDiscarded, map[string]any{})
}

// dial opens a new connection under an already-reserved slot. Pooled clients
// always self-heal: AutoReconnect lets a client whose socket dropped between
// commands reconnect on its next use instead of being discarded, so a transient
// drop is absorbed on the same slot.
func (p *Pool) dial(ctx context.Context) (*pooledConn, error) {
	cfg := p.cfg.ClientConfig
	cfg.AutoReconnect = true
	client := NewClientWithConfig(cfg)
	if err := client.ConnectContext(ctx); err != nil {
		return nil, err
	}
	now := time.Now()
	return &pooledConn{client: client, createdAt: now, idleSince: now}, nil
}

// expired reports whether a connection has exceeded its lifetime or idle limit.
func (p *Pool) expired(pc *pooledConn) bool {
	now := time.Now()
	if p.cfg.ConnMaxLifetime > 0 && now.Sub(pc.createdAt) >= p.cfg.ConnMaxLifetime {
		return true
	}
	if p.cfg.ConnMaxIdleTime > 0 && now.Sub(pc.idleSince) >= p.cfg.ConnMaxIdleTime {
		return true
	}
	return false
}
