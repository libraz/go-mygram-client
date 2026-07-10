package mygram

import (
	"context"
	"testing"
)

func TestConnReusable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, true},
		{"server error", &ServerError{Message: "table not found"}, true},
		{"validation error", &ValidationError{Message: "bad table"}, true},
		{"connection error", &ConnectionError{Message: "read failed"}, false},
		{"timeout error", &TimeoutError{Message: "command timeout"}, false},
		{"protocol error", &ProtocolError{Message: "too big"}, false},
		{"pre-send context canceled", context.Canceled, true},
		{"pre-send context deadline", context.DeadlineExceeded, true},
		// A post-send read timeout wraps DeadlineExceeded but must NOT be
		// reusable: the socket has an outstanding response. Guards against a
		// naive errors.Is check that would misclassify it.
		{"post-send timeout wrapping deadline", &TimeoutError{Message: "command timeout", Err: context.DeadlineExceeded}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connReusable(tt.err); got != tt.want {
				t.Errorf("connReusable(%T) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestShouldRetry(t *testing.T) {
	connBeforeSend := &ConnectionError{Message: "write failed", beforeSend: true}
	connAfterSend := &ConnectionError{Message: "read failed"}

	tests := []struct {
		name       string
		err        error
		idempotent bool
		attempt    int
		maxRetries int
		want       bool
	}{
		{"before-send idempotent", connBeforeSend, true, 0, 1, true},
		{"before-send mutating still retried", connBeforeSend, false, 0, 1, true},
		{"after-send idempotent retried", connAfterSend, true, 0, 1, true},
		{"after-send mutating not retried", connAfterSend, false, 0, 1, false},
		{"budget exhausted", connAfterSend, true, 1, 1, false},
		{"zero budget", connBeforeSend, true, 0, 0, false},
		{"server error never retried", &ServerError{Message: "x"}, true, 0, 3, false},
		{"validation error never retried", &ValidationError{Message: "x"}, true, 0, 3, false},
		{"timeout never retried", &TimeoutError{Message: "x"}, true, 0, 3, false},
		{"nil never retried", nil, true, 0, 3, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRetry(tt.err, tt.idempotent, tt.attempt, tt.maxRetries)
			if got != tt.want {
				t.Errorf("shouldRetry(%v, idempotent=%v, attempt=%d, max=%d) = %v, want %v",
					tt.err, tt.idempotent, tt.attempt, tt.maxRetries, got, tt.want)
			}
		})
	}
}

func TestNewPoolDefaults(t *testing.T) {
	p := NewPool(PoolConfig{})
	if p.cfg.MaxOpenConns != defaultMaxOpenConns {
		t.Errorf("MaxOpenConns = %d, want %d", p.cfg.MaxOpenConns, defaultMaxOpenConns)
	}
	if p.cfg.MaxIdleConns != defaultMaxOpenConns {
		t.Errorf("MaxIdleConns = %d, want %d", p.cfg.MaxIdleConns, defaultMaxOpenConns)
	}
	if p.cfg.Retry.MaxRetries != defaultMaxRetries {
		t.Errorf("MaxRetries = %d, want %d", p.cfg.Retry.MaxRetries, defaultMaxRetries)
	}
	if p.cfg.Retry.BaseBackoff != defaultBaseBackoff {
		t.Errorf("BaseBackoff = %v, want %v", p.cfg.Retry.BaseBackoff, defaultBaseBackoff)
	}
	if cap(p.sem) != defaultMaxOpenConns {
		t.Errorf("cap(sem) = %d, want %d", cap(p.sem), defaultMaxOpenConns)
	}
}

func TestNewPoolClampsIdleToOpen(t *testing.T) {
	p := NewPool(PoolConfig{MaxOpenConns: 3, MaxIdleConns: 10})
	if p.cfg.MaxIdleConns != 3 {
		t.Errorf("MaxIdleConns = %d, want clamped to 3", p.cfg.MaxIdleConns)
	}
}

func TestNewPoolExplicitNoRetry(t *testing.T) {
	// A caller that sets a backoff but wants zero retries must keep zero.
	p := NewPool(PoolConfig{Retry: RetryPolicy{MaxRetries: 0, BaseBackoff: 10}})
	if p.cfg.Retry.MaxRetries != 0 {
		t.Errorf("MaxRetries = %d, want 0 (explicit)", p.cfg.Retry.MaxRetries)
	}
}

func TestNewPoolNegativeMaxRetriesDisables(t *testing.T) {
	// A negative MaxRetries is the intuitive opt-out and must stay at zero
	// rather than being re-inflated to the default policy.
	p := NewPool(PoolConfig{Retry: RetryPolicy{MaxRetries: -1}})
	if p.cfg.Retry.MaxRetries != 0 {
		t.Errorf("MaxRetries = %d, want 0 (negative disables retries)", p.cfg.Retry.MaxRetries)
	}
}
