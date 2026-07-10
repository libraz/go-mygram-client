package mygram

import (
	"context"
	"math/rand"
	"time"
)

// contextReusable reports whether err is a bare context error returned before
// anything was written to the socket. Such an error only originates from the
// fast fail in SendCommandContext (the caller's context was already done), so
// the connection was never touched and is safe to reuse. A read-phase deadline
// is surfaced as a *TimeoutError instead, so an identity comparison here — not
// errors.Is, which would also match a *TimeoutError wrapping DeadlineExceeded —
// keeps a post-send timeout correctly classified as not reusable.
func contextReusable(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}

// maxBackoff caps the per-attempt backoff delay.
const maxBackoff = 30 * time.Second

// connReusable reports whether a connection is still healthy after a command
// returned err, and therefore may be returned to the pool. A nil error, a
// server-side ERROR response, a client-side validation failure, and a
// pre-send context cancellation all leave the connection usable; connection,
// timeout, and protocol failures do not.
func connReusable(err error) bool {
	if err == nil {
		return true
	}
	switch err.(type) {
	case *ServerError, *ValidationError:
		return true
	}
	// A borrow with an already-canceled or already-expired context fails fast
	// before writing anything, leaving the connection pristine; keep it.
	return contextReusable(err)
}

// shouldRetry decides whether a failed command may be retried. It never retries
// server errors, validation errors, or post-send timeouts. A connection error
// or a connect-phase (pre-send) timeout is retried when the command never
// reached the server (safe for any command) or when the command is idempotent,
// subject to the attempt budget.
func shouldRetry(err error, idempotent bool, attempt, maxRetries int) bool {
	if attempt >= maxRetries {
		return false
	}
	switch e := err.(type) {
	case *ConnectionError:
		if e.beforeSend {
			return true
		}
		return idempotent
	case *TimeoutError:
		// A connect-phase timeout wrote nothing, so a fresh dial is safe for any
		// command; a post-send read timeout may have been acted upon and is not
		// retried.
		return e.beforeSend
	default:
		return false
	}
}

// sleepBackoff waits for an exponential-with-full-jitter delay before the next
// attempt, or returns early if the context is canceled. attempt is 1-based.
func (p *Pool) sleepBackoff(ctx context.Context, attempt int) error {
	base := p.cfg.Retry.BaseBackoff
	if base <= 0 {
		base = defaultBaseBackoff
	}
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 20 {
		shift = 20
	}
	d := base << shift
	if d <= 0 || d > maxBackoff {
		d = maxBackoff
	}
	// Full jitter: a uniformly random delay in [0, d].
	jitter := time.Duration(rand.Int63n(int64(d) + 1))
	timer := time.NewTimer(jitter)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
