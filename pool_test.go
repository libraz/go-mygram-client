package mygram

import (
	"bufio"
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// poolMockServer starts a listener whose accepted connections are handled by
// handle. It returns the address, an accessor for the number of accepted
// connections, and a cleanup function.
func poolMockServer(t *testing.T, handle func(idx int, conn net.Conn)) (addr string, accepts func() int, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			idx := int(atomic.AddInt64(&n, 1))
			go handle(idx, conn)
		}
	}()
	return ln.Addr().String(), func() int { return int(atomic.LoadInt64(&n)) }, func() { _ = ln.Close() }
}

// waitAccepts blocks until the mock server's accept count reaches want, failing
// the test if it overshoots or does not arrive within the deadline. The count
// is incremented in the server's accept loop, which runs after the client's
// dial has returned, so reading it immediately would race; polling closes that
// window without masking a genuine extra connection (which pushes the count
// past want and fails fast).
func waitAccepts(t *testing.T, accepts func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := accepts()
		if got == want {
			return
		}
		if got > want {
			t.Fatalf("accepted %d connections, want %d", got, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf("accepted %d connections, want %d (timed out)", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// echoSearchServer serves any number of SEARCH-like requests on one connection,
// replying with a fixed single-result response, until the peer closes.
func echoSearchServer(idx int, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	for {
		if _, err := br.ReadString('\n'); err != nil {
			return
		}
		if _, err := conn.Write([]byte("OK RESULTS 1 42\r\n")); err != nil {
			return
		}
	}
}

func poolFor(t *testing.T, addr string, cfg PoolConfig) *Pool {
	t.Helper()
	cfg.Host, cfg.Port = splitHostPort(t, addr)
	cfg.MaxQueryLength = 0
	p := NewPool(cfg)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	c := NewClient(addr)
	return c.config.Host, c.config.Port
}

func TestPoolBasicSearch(t *testing.T) {
	addr, _, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	p := poolFor(t, addr, PoolConfig{MaxOpenConns: 4})
	resp, err := p.Search("threads", "keyword", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 1 || len(resp.Results) != 1 || resp.Results[0].PrimaryKey != "42" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestPoolReusesConnection(t *testing.T) {
	addr, accepts, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	p := poolFor(t, addr, PoolConfig{MaxOpenConns: 4})
	for i := 0; i < 5; i++ {
		if _, err := p.Search("threads", "k", SearchOptions{Limit: 1}); err != nil {
			t.Fatal(err)
		}
	}
	waitAccepts(t, accepts, 1) // sequential calls should reuse
	st := p.Stats()
	if st.Open != 1 || st.Idle != 1 || st.InUse != 0 {
		t.Errorf("stats = %+v, want Open=1 Idle=1 InUse=0", st)
	}
}

func TestPoolRespectsMaxOpenConns(t *testing.T) {
	var concurrent, maxConcurrent int32
	handle := func(idx int, conn net.Conn) {
		cur := atomic.AddInt32(&concurrent, 1)
		for {
			m := atomic.LoadInt32(&maxConcurrent)
			if cur <= m || atomic.CompareAndSwapInt32(&maxConcurrent, m, cur) {
				break
			}
		}
		defer func() { atomic.AddInt32(&concurrent, -1); _ = conn.Close() }()
		br := bufio.NewReader(conn)
		for {
			if _, err := br.ReadString('\n'); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond) // hold the request to induce concurrency
			if _, err := conn.Write([]byte("OK RESULTS 1 42\r\n")); err != nil {
				return
			}
		}
	}
	addr, _, cleanup := poolMockServer(t, handle)
	defer cleanup()

	const maxOpen = 4
	p := poolFor(t, addr, PoolConfig{MaxOpenConns: maxOpen})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Search("threads", "k", SearchOptions{Limit: 1}); err != nil {
				t.Errorf("search failed: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxConcurrent); got > maxOpen {
		t.Errorf("max concurrent connections = %d, want <= %d", got, maxOpen)
	}
}

func TestPoolContextCancelWhenExhausted(t *testing.T) {
	addr, _, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	p := poolFor(t, addr, PoolConfig{MaxOpenConns: 1})

	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = p.WithConn(context.Background(), func(c *Client) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := p.SearchContext(ctx, "threads", "k", SearchOptions{Limit: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestPoolClosed(t *testing.T) {
	addr, _, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	p := poolFor(t, addr, PoolConfig{MaxOpenConns: 2})
	if _, err := p.Search("threads", "k", SearchOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	// Idempotent Close.
	if err := p.Close(); err != nil {
		t.Errorf("second Close returned %v", err)
	}
	_, err := p.Search("threads", "k", SearchOptions{Limit: 1})
	if _, ok := err.(*ConnectionError); !ok {
		t.Errorf("after Close, err = %T (%v), want *ConnectionError", err, err)
	}
	if st := p.Stats(); st.Open != 0 {
		t.Errorf("after Close, Open = %d, want 0", st.Open)
	}
}

// dropFirstThenServe closes the first accepted connection without responding
// (simulating a dropped/stale connection) and serves later connections
// normally.
func dropFirstThenServe(idx int, conn net.Conn) {
	br := bufio.NewReader(conn)
	if _, err := br.ReadString('\n'); err != nil {
		_ = conn.Close()
		return
	}
	if idx == 1 {
		_ = conn.Close() // drop after reading, before responding
		return
	}
	_, _ = conn.Write([]byte("OK RESULTS 1 42\r\n"))
	echoSearchServer(idx, conn)
}

func TestPoolRetriesIdempotentAfterDrop(t *testing.T) {
	addr, accepts, cleanup := poolMockServer(t, dropFirstThenServe)
	defer cleanup()

	p := poolFor(t, addr, PoolConfig{MaxOpenConns: 2})
	resp, err := p.Search("threads", "k", SearchOptions{Limit: 1})
	if err != nil {
		t.Fatalf("search should have succeeded after retry, got %v", err)
	}
	if resp.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1", resp.TotalCount)
	}
	waitAccepts(t, accepts, 2) // one dropped, one retried
	if st := p.Stats(); st.Retries != 1 {
		t.Errorf("Retries = %d, want 1", st.Retries)
	}
}

func TestPoolDoesNotRetryMutatingAfterDrop(t *testing.T) {
	addr, accepts, cleanup := poolMockServer(t, dropFirstThenServe)
	defer cleanup()

	p := poolFor(t, addr, PoolConfig{MaxOpenConns: 2})
	// Optimize is state-changing: a read-phase failure must not be retried.
	err := p.Optimize("threads")
	if _, ok := err.(*ConnectionError); !ok {
		t.Fatalf("err = %T (%v), want *ConnectionError", err, err)
	}
	waitAccepts(t, accepts, 1) // no retry for mutating command
}

func TestPoolDiscardsExpiredOnBorrow(t *testing.T) {
	addr, accepts, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	p := poolFor(t, addr, PoolConfig{MaxOpenConns: 2, ConnMaxLifetime: time.Millisecond})
	if _, err := p.Search("threads", "k", SearchOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	// Let the parked connection outlive ConnMaxLifetime, then borrow again: the
	// expired connection must be discarded and a fresh one dialed.
	time.Sleep(10 * time.Millisecond)
	if _, err := p.Search("threads", "k", SearchOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	waitAccepts(t, accepts, 2) // expired conn discarded, new one dialed
	if st := p.Stats(); st.Discarded < 1 {
		t.Errorf("Discarded = %d, want >= 1", st.Discarded)
	}
}

func TestPoolReaperDiscardsIdleWithoutBorrow(t *testing.T) {
	addr, _, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	p := poolFor(t, addr, PoolConfig{MaxOpenConns: 2, ConnMaxIdleTime: 30 * time.Millisecond})
	if _, err := p.Search("threads", "k", SearchOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if st := p.Stats(); st.Idle != 1 {
		t.Fatalf("Idle = %d, want 1 after first search", st.Idle)
	}

	// With no further borrow, the background reaper must discard the idle
	// connection once it outlives ConnMaxIdleTime.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.Stats().Open == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st := p.Stats(); st.Open != 0 {
		t.Errorf("Open = %d, want 0 (reaper should have discarded the idle conn)", st.Open)
	}
	if st := p.Stats(); st.Discarded < 1 {
		t.Errorf("Discarded = %d, want >= 1", st.Discarded)
	}
}

func TestReapInterval(t *testing.T) {
	cases := []struct {
		name string
		cfg  PoolConfig
		want time.Duration
	}{
		{"no limits", PoolConfig{}, 0},
		{"idle only", PoolConfig{ConnMaxIdleTime: time.Minute}, time.Minute},
		{"lifetime only", PoolConfig{ConnMaxLifetime: 2 * time.Minute}, 2 * time.Minute},
		{"shorter of the two", PoolConfig{ConnMaxLifetime: 5 * time.Minute, ConnMaxIdleTime: time.Minute}, time.Minute},
		{"floored", PoolConfig{ConnMaxIdleTime: time.Millisecond}, minReapInterval},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reapInterval(tc.cfg); got != tc.want {
				t.Errorf("reapInterval = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPoolShedsIdleAboveMaxIdle(t *testing.T) {
	// The server holds each request briefly to force real concurrency, so a burst
	// opens more connections at once than the idle pool is allowed to retain.
	handle := func(idx int, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		br := bufio.NewReader(conn)
		for {
			if _, err := br.ReadString('\n'); err != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
			if _, err := conn.Write([]byte("OK RESULTS 1 42\r\n")); err != nil {
				return
			}
		}
	}
	addr, _, cleanup := poolMockServer(t, handle)
	defer cleanup()

	const maxOpen, maxIdle = 6, 2
	p := poolFor(t, addr, PoolConfig{MaxOpenConns: maxOpen, MaxIdleConns: maxIdle})

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Search("threads", "k", SearchOptions{Limit: 1}); err != nil {
				t.Errorf("search failed: %v", err)
			}
		}()
	}
	wg.Wait()

	// Once the burst drains, the pool must retain at most MaxIdleConns and have
	// discarded the surplus instead of parking every connection it opened.
	st := p.Stats()
	if st.InUse != 0 {
		t.Errorf("InUse = %d after the burst drained, want 0", st.InUse)
	}
	if st.Idle > maxIdle || st.Open > maxIdle {
		t.Errorf("stats = %+v, want Idle and Open <= %d (surplus should be shed)", st, maxIdle)
	}
	if st.Discarded < 1 {
		t.Errorf("Discarded = %d, want >= 1 (a burst beyond MaxIdleConns must shed connections)", st.Discarded)
	}
}

func TestPoolDisabledRetryDoesNotRetry(t *testing.T) {
	addr, accepts, cleanup := poolMockServer(t, dropFirstThenServe)
	defer cleanup()

	// A negative MaxRetries disables retries entirely, so even an idempotent
	// command surfaces the first connection's read-phase drop.
	p := poolFor(t, addr, PoolConfig{MaxOpenConns: 2, Retry: RetryPolicy{MaxRetries: -1}})
	_, err := p.Search("threads", "k", SearchOptions{Limit: 1})
	if _, ok := err.(*ConnectionError); !ok {
		t.Fatalf("err = %T (%v), want *ConnectionError (retries disabled)", err, err)
	}
	waitAccepts(t, accepts, 1) // no retry when disabled
	if st := p.Stats(); st.Retries != 0 {
		t.Errorf("Retries = %d, want 0", st.Retries)
	}
}

func TestPoolReusesConnAfterPreSendContextCancel(t *testing.T) {
	addr, accepts, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	p := poolFor(t, addr, PoolConfig{MaxOpenConns: 2})
	// Warm one connection into the idle pool.
	if _, err := p.Search("threads", "k", SearchOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}

	// A request whose context is already canceled fails fast before touching the
	// socket, so the borrowed connection must be returned to the pool, not
	// discarded.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.SearchContext(ctx, "threads", "k", SearchOptions{Limit: 1}); err == nil {
		t.Fatal("expected a context error")
	}
	if st := p.Stats(); st.Discarded != 0 {
		t.Errorf("Discarded = %d, want 0 (healthy conn must be reused)", st.Discarded)
	}
	// A follow-up succeeds on the reused connection without a new dial.
	if _, err := p.Search("threads", "k", SearchOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	waitAccepts(t, accepts, 1) // connection should have been reused
}

func TestPoolReleaseAfterCloseDiscards(t *testing.T) {
	addr, _, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	p := poolFor(t, addr, PoolConfig{MaxOpenConns: 2})

	// Borrow a connection and, while it is in use, close the pool. When the
	// borrow returns, release must discard the connection rather than parking
	// it in the now-closed pool (which would leak the fd and its sem slot).
	started := make(chan struct{})
	proceed := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = p.WithConn(context.Background(), func(c *Client) error {
			close(started)
			<-proceed
			return nil
		})
		close(done)
	}()
	<-started
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	<-done

	if st := p.Stats(); st.Open != 0 {
		t.Errorf("after Close and release, Open = %d, want 0 (connection leaked)", st.Open)
	}
}

func TestPoolDoesNotDialAfterClose(t *testing.T) {
	addr, accepts, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	p := poolFor(t, addr, PoolConfig{MaxOpenConns: 1})

	// Occupy the only slot, park a second borrow in the slow path, then close.
	started := make(chan struct{})
	proceed := make(chan struct{})
	go func() {
		_ = p.WithConn(context.Background(), func(c *Client) error {
			close(started)
			<-proceed
			return nil
		})
	}()
	<-started

	waiterErr := make(chan error, 1)
	go func() {
		_, err := p.SearchContext(context.Background(), "threads", "k", SearchOptions{Limit: 1})
		waiterErr <- err
	}()
	// Give the waiter time to reach the slow-path select.
	time.Sleep(20 * time.Millisecond)

	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	close(proceed) // in-use conn returns, frees the slot, waking the waiter

	select {
	case err := <-waiterErr:
		if _, ok := err.(*ConnectionError); !ok {
			t.Errorf("waiter err = %T (%v), want *ConnectionError from a closed pool", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not return after Close")
	}
	waitAccepts(t, accepts, 1) // must not dial after Close
}

func TestPoolRetriesDialFailure(t *testing.T) {
	// Reserve an address, then release it so the first dial is refused.
	ln0, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln0.Addr().String()
	_ = ln0.Close() // nothing is listening now → connection refused

	host, port := splitHostPort(t, addr)
	p := NewPool(PoolConfig{
		ClientConfig: ClientConfig{Host: host, Port: port, TimeoutMs: 500},
		MaxOpenConns: 2,
		Retry:        RetryPolicy{MaxRetries: 20, BaseBackoff: 5 * time.Millisecond},
	})
	t.Cleanup(func() { _ = p.Close() })

	// Start listening shortly after, so early dials fail and a later retry wins.
	lnCh := make(chan net.Listener, 1)
	go func() {
		time.Sleep(40 * time.Millisecond)
		ln, lerr := net.Listen("tcp", addr)
		if lerr != nil {
			lnCh <- nil
			return
		}
		lnCh <- ln
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go echoSearchServer(0, conn)
		}
	}()
	t.Cleanup(func() {
		if ln := <-lnCh; ln != nil {
			_ = ln.Close()
		}
	})

	resp, err := p.Search("threads", "k", SearchOptions{Limit: 1})
	if err != nil {
		t.Fatalf("expected success after dial retries, got %v", err)
	}
	if resp.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1", resp.TotalCount)
	}
	if p.Stats().Retries == 0 {
		t.Error("expected at least one retry for the refused dials")
	}
}

func TestPoolDialFailureExhaustsRetries(t *testing.T) {
	// A permanently unavailable server must surface a ConnectionError after the
	// retry budget is spent, rather than blocking forever.
	ln0, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln0.Addr().String()
	_ = ln0.Close()

	host, port := splitHostPort(t, addr)
	p := NewPool(PoolConfig{
		ClientConfig: ClientConfig{Host: host, Port: port, TimeoutMs: 200},
		MaxOpenConns: 1,
		Retry:        RetryPolicy{MaxRetries: 2, BaseBackoff: time.Millisecond},
	})
	t.Cleanup(func() { _ = p.Close() })

	_, err = p.Search("threads", "k", SearchOptions{Limit: 1})
	if _, ok := err.(*ConnectionError); !ok {
		t.Fatalf("err = %T (%v), want *ConnectionError", err, err)
	}
	if got := p.Stats().Retries; got != 2 {
		t.Errorf("Retries = %d, want 2 (MaxRetries)", got)
	}
}

// TestPoolMatchesClientContextMethods verifies that every command-bearing
// *Context method on Client has a counterpart on Pool with an identical
// signature (receiver aside), so the two surfaces cannot drift apart.
func TestPoolMatchesClientContextMethods(t *testing.T) {
	// Lifecycle methods that intentionally do not appear on Pool.
	exclude := map[string]bool{
		"ConnectContext": true,
	}

	clientT := reflect.TypeOf((*Client)(nil))
	poolT := reflect.TypeOf((*Pool)(nil))

	for i := 0; i < clientT.NumMethod(); i++ {
		m := clientT.Method(i)
		if !strings.HasSuffix(m.Name, "Context") || exclude[m.Name] {
			continue
		}
		pm, ok := poolT.MethodByName(m.Name)
		if !ok {
			t.Errorf("Pool is missing Client method %s", m.Name)
			continue
		}
		if !sameSignatureExceptReceiver(m.Func.Type(), pm.Func.Type()) {
			t.Errorf("Pool.%s signature does not match Client.%s", m.Name, m.Name)
		}
	}
}

// sameSignatureExceptReceiver compares two method func types ignoring the
// receiver (parameter 0 of each).
func sameSignatureExceptReceiver(a, b reflect.Type) bool {
	if a.NumIn() != b.NumIn() || a.NumOut() != b.NumOut() {
		return false
	}
	for i := 1; i < a.NumIn(); i++ {
		if a.In(i) != b.In(i) {
			return false
		}
	}
	for i := 0; i < a.NumOut(); i++ {
		if a.Out(i) != b.Out(i) {
			return false
		}
	}
	return true
}
