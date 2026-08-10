package mygram

import (
	"context"
	"testing"
)

// Retry and connection-reuse classification (retry.go).

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
		{"post-send timeout not retried", &TimeoutError{Message: "x"}, true, 0, 3, false},
		{"connect-phase timeout idempotent retried", &TimeoutError{Message: "connection timeout", beforeSend: true}, true, 0, 3, true},
		{"connect-phase timeout mutating retried", &TimeoutError{Message: "connection timeout", beforeSend: true}, false, 0, 3, true},
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

func TestShouldRetryTransientServerErrors(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		idempotent bool
		want       bool
	}{
		{"busy is retried", &ServerError{Code: ErrCodeServerBusy}, true, true},
		{"loading is retried", &ServerError{Code: ErrCodeServerLoading}, true, true},
		{"not ready is retried", &ServerError{Code: ErrCodeServerNotReady}, true, true},
		{"busy is retried for a state-changing command too", &ServerError{Code: ErrCodeServerBusy}, false, true},
		{"table not found is not retried", &ServerError{Code: ErrCodeTableNotFound}, true, false},
		{"uncoded frame is not retried", &ServerError{Message: "server is busy"}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRetry(tc.err, tc.idempotent, 0, 1); got != tc.want {
				t.Errorf("shouldRetry = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTransientServerErrorRespectsAttemptBudget(t *testing.T) {
	err := &ServerError{Code: ErrCodeServerBusy}
	if shouldRetry(err, true, 1, 1) {
		t.Error("shouldRetry ignored the exhausted attempt budget")
	}
}

// A connection that returned a server error is still healthy, so a transient
// rejection must not cost the pool its socket.
func TestTransientServerErrorKeepsConnection(t *testing.T) {
	if !connReusable(&ServerError{Code: ErrCodeServerBusy}) {
		t.Error("connReusable = false for a server error, want true")
	}
}
