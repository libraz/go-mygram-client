package mygram

import (
	"errors"
	"strconv"
	"strings"
)

// ErrorCode is the numeric code carried by a MygramDB ERROR frame.
//
// Since MygramDB v1.10 every TCP ERROR frame is "ERROR <code> <message>", where
// <code> is a decimal uint16 drawn from the server's shared error-code space.
// Older servers send an uncoded "ERROR <message>" frame; those are reported as
// ErrCodeNone so a client can tell "no code" from a real code.
//
// The constants below name the codes a client can act on. Any uint16 may appear
// on the wire, so treat an unrecognized value as an opaque server error rather
// than an unexpected condition.
type ErrorCode uint16

// General codes (0-999).
const (
	// ErrCodeNone means the ERROR frame carried no numeric code, which is how
	// a pre-v1.10 server reports every failure.
	ErrCodeNone ErrorCode = 0

	ErrCodeUnknown          ErrorCode = 1
	ErrCodeInvalidArgument  ErrorCode = 2
	ErrCodeOutOfRange       ErrorCode = 3
	ErrCodeNotImplemented   ErrorCode = 4
	ErrCodeInternalError    ErrorCode = 5
	ErrCodeIOError          ErrorCode = 6
	ErrCodePermissionDenied ErrorCode = 7
	ErrCodeNotFound         ErrorCode = 8
	ErrCodeAlreadyExists    ErrorCode = 9
	ErrCodeTimeout          ErrorCode = 10
	ErrCodeCancelled        ErrorCode = 11
)

// Query codes (3000-3999).
const (
	ErrCodeQuerySyntaxError          ErrorCode = 3000
	ErrCodeQueryInvalidToken         ErrorCode = 3001
	ErrCodeQueryUnexpectedToken      ErrorCode = 3002
	ErrCodeQueryMissingOperand       ErrorCode = 3003
	ErrCodeQueryInvalidOperator      ErrorCode = 3004
	ErrCodeQueryTooLong              ErrorCode = 3005
	ErrCodeQueryInvalidFilter        ErrorCode = 3006
	ErrCodeQueryInvalidSort          ErrorCode = 3007
	ErrCodeQueryInvalidLimit         ErrorCode = 3008
	ErrCodeQueryInvalidOffset        ErrorCode = 3009
	ErrCodeQueryExpressionParseError ErrorCode = 3010
	ErrCodeQueryASTBuildError        ErrorCode = 3011
)

// Index and catalog codes (4000-4999).
const (
	ErrCodeIndexDocumentNotFound ErrorCode = 4004
	ErrCodeTableNotFound         ErrorCode = 4007
	ErrCodeCatalogNotInitialized ErrorCode = 4008
	ErrCodeSyncTableNotFound     ErrorCode = 4010
	ErrCodeSyncAlreadyInProgress ErrorCode = 4011
	ErrCodeSyncMemoryCritical    ErrorCode = 4012
)

// Network and server codes (6000-6999).
const (
	ErrCodeNetworkInvalidRequest ErrorCode = 6007
	ErrCodeNetworkProtocolError  ErrorCode = 6008

	// ErrCodeServerShuttingDown rejects a new long-running operation while the
	// server is stopping. Retrying against the same server will not help.
	ErrCodeServerShuttingDown ErrorCode = 6027

	// ErrCodeServerLoading means the server is restoring a dump and is
	// temporarily unavailable. The command was not executed.
	ErrCodeServerLoading ErrorCode = 6028

	// ErrCodeServerNotReady means the server has not finished building the
	// state the command needs. The command was not executed.
	ErrCodeServerNotReady ErrorCode = 6029

	// ErrCodeServerBusy means request capacity is temporarily exhausted (for
	// example the rate limiter denied the request). The command was not
	// executed.
	ErrCodeServerBusy ErrorCode = 6030
)

// Cache codes (8000-8999).
const (
	ErrCodeCacheDisabled ErrorCode = 8001
)

// Retryable reports whether the code names a transient server condition that
// rejected the command before executing it, so re-issuing the same command
// (after a backoff) is both safe and worth doing. Every other code describes a
// deterministic failure that a retry would only repeat.
func (c ErrorCode) Retryable() bool {
	switch c {
	case ErrCodeServerLoading, ErrCodeServerNotReady, ErrCodeServerBusy:
		return true
	default:
		return false
	}
}

// ServerErrorCode reports the numeric code of a server ERROR frame. It returns
// (ErrCodeNone, false) for any error that is not a *ServerError, and
// (ErrCodeNone, true) for an uncoded frame from a pre-v1.10 server. The error
// chain is unwrapped, so a *ServerError still reports its code after a caller
// has wrapped it with fmt.Errorf and %w.
func ServerErrorCode(err error) (ErrorCode, bool) {
	var se *ServerError
	if errors.As(err, &se) {
		return se.Code, true
	}
	return ErrCodeNone, false
}

// errorPrefix is the wire prefix of every ERROR frame.
const errorPrefix = "ERROR "

// parseErrorFrame decodes a coded or legacy single-line ERROR frame, mirroring
// the server's protocol::ParseErrorFrame.
//
// Since MygramDB v1.10 the payload starts with a decimal uint16 code followed
// by a space. The leading token is treated as a code only when it is non-zero
// and parses in full, so an uncoded frame from an older server — or one whose
// message happens to start with a word — keeps its whole payload as the
// message.
func parseErrorFrame(response string) *ServerError {
	payload := response[len(errorPrefix):]
	token, rest, hasSeparator := strings.Cut(payload, " ")
	code, err := strconv.ParseUint(token, 10, 16)
	if err != nil || code == 0 {
		return &ServerError{Message: payload}
	}
	if !hasSeparator {
		rest = ""
	}
	return &ServerError{Message: rest, Code: ErrorCode(code)}
}
