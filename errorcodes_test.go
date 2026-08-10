package mygram

import (
	"errors"
	"fmt"
	"testing"
)

// The numeric error-code surface (errorcodes.go): ERROR frame decoding, the
// retryable classification, and the ServerErrorCode accessor.

func TestParseErrorFrame(t *testing.T) {
	cases := []struct {
		name        string
		frame       string
		wantCode    ErrorCode
		wantMessage string
	}{
		{"coded frame", "ERROR 4007 Table not found: articles", ErrCodeTableNotFound, "Table not found: articles"},
		{"coded frame without message", "ERROR 6030", ErrCodeServerBusy, ""},
		{"legacy uncoded frame", "ERROR Table not found", ErrCodeNone, "Table not found"},
		{"zero is not a code", "ERROR 0 something", ErrCodeNone, "0 something"},
		{"non-numeric leading token", "ERROR 12abc broken", ErrCodeNone, "12abc broken"},
		{"signed token is not a code", "ERROR +5 broken", ErrCodeNone, "+5 broken"},
		{"out-of-range token", "ERROR 70000 broken", ErrCodeNone, "70000 broken"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := parseErrorFrame(tc.frame)
			if err.Code != tc.wantCode {
				t.Errorf("Code = %d, want %d", err.Code, tc.wantCode)
			}
			if err.Message != tc.wantMessage {
				t.Errorf("Message = %q, want %q", err.Message, tc.wantMessage)
			}
		})
	}
}

func TestErrorCodeRetryable(t *testing.T) {
	retryable := []ErrorCode{ErrCodeServerLoading, ErrCodeServerNotReady, ErrCodeServerBusy}
	for _, code := range retryable {
		if !code.Retryable() {
			t.Errorf("ErrorCode(%d).Retryable() = false, want true", code)
		}
	}
	permanent := []ErrorCode{
		ErrCodeNone, ErrCodeTableNotFound, ErrCodeQuerySyntaxError,
		ErrCodePermissionDenied, ErrCodeServerShuttingDown, ErrCodeInternalError,
	}
	for _, code := range permanent {
		if code.Retryable() {
			t.Errorf("ErrorCode(%d).Retryable() = true, want false", code)
		}
	}
}

func TestServerErrorCodeIgnoresOtherErrors(t *testing.T) {
	if code, ok := ServerErrorCode(errors.New("boom")); ok || code != ErrCodeNone {
		t.Errorf("ServerErrorCode = (%d, %v), want (0, false)", code, ok)
	}
}

func TestServerErrorCodeUnwrapsWrappedError(t *testing.T) {
	wrapped := fmt.Errorf("search failed: %w", &ServerError{Message: "busy", Code: ErrCodeServerBusy})
	code, ok := ServerErrorCode(wrapped)
	if !ok || code != ErrCodeServerBusy {
		t.Errorf("ServerErrorCode = (%d, %v), want (%d, true)", code, ok, ErrCodeServerBusy)
	}
}
