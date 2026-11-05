//go:build e2e
// +build e2e

// Package mygram e2e tests run against a live MygramDB server.
//
// Run:
//
//	go test -tags=e2e -count=1 -v ./...
//
// The server endpoint can be overridden via MYGRAM_HOST / MYGRAM_PORT.
// Tests that mutate global server state (replication start/stop, cache
// enable/disable, dump save/load) restore the original state on exit so
// they remain idempotent. If the server is unreachable, every test is
// skipped via t.Skip rather than failing.
package mygram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	defaultE2EHost = "127.0.0.1"
	defaultE2EPort = 11016
)

// e2eAddr resolves the server address from MYGRAM_HOST / MYGRAM_PORT.
func e2eAddr() (string, int) {
	host := os.Getenv("MYGRAM_HOST")
	if host == "" {
		host = defaultE2EHost
	}
	port := defaultE2EPort
	if v := os.Getenv("MYGRAM_PORT"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			port = parsed
		}
	}
	return host, port
}

// e2eAdminToken returns the administrative token for the server under test.
// A server that sets api.admin_token (mandatory from v1.10 on a non-loopback
// bind) gates DUMP, SYNC, REPLICATION, CACHE and CONFIG behind AUTH, so the
// docker harness passes its token through MYGRAM_ADMIN_TOKEN. An empty value
// sends no AUTH, which is what a loopback-only server without a token wants.
func e2eAdminToken() string {
	return os.Getenv("MYGRAM_ADMIN_TOKEN")
}

// e2eServerAvailable probes the configured endpoint by issuing INFO.
// Cached so repeated tests pay only a single round-trip.
var (
	e2eAvailable     bool
	e2eAvailableOnce bool
)

func ensureE2EServer(tb testing.TB) {
	tb.Helper()
	if !e2eAvailableOnce {
		host, port := e2eAddr()
		c := NewClientWithConfig(ClientConfig{
			Host:           host,
			Port:           port,
			TimeoutMs:      2000,
			RecvBufferSize: 65536,
			MaxQueryLength: 128,
			AdminToken:     e2eAdminToken(),
		})
		if err := c.Connect(); err == nil {
			if _, err := c.Info(); err == nil {
				e2eAvailable = true
			}
			_ = c.Close()
		}
		e2eAvailableOnce = true
	}
	if !e2eAvailable {
		host, port := e2eAddr()
		// Against an arbitrary developer machine an absent server is a reason
		// to skip. Under the docker harness it is a failure: that harness
		// booted a server and waited for it, so a client that cannot reach it
		// is a client bug — and skipping every test would exit 0 and read as a
		// pass.
		if os.Getenv("MYGRAM_E2E_SEEDED") == "1" {
			tb.Fatalf("mygramdb server not reachable at %s:%d although the docker harness started one", host, port)
		}
		tb.Skipf("mygramdb server not reachable at %s:%d (set MYGRAM_HOST/MYGRAM_PORT or start the server)", host, port)
	}
}

// newE2EClient connects to the configured server and registers Close on cleanup.
func newE2EClient(t *testing.T) *Client {
	t.Helper()
	ensureE2EServer(t)
	host, port := e2eAddr()
	c := NewClientWithConfig(ClientConfig{
		Host:           host,
		Port:           port,
		TimeoutMs:      5000,
		RecvBufferSize: 65536,
		MaxQueryLength: 0,
		AdminToken:     e2eAdminToken(),
	})
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// firstTable returns the first available table name, or skips when the
// server has no tables configured (most e2e flows are data-shape agnostic
// but require at least one table to exercise SEARCH/COUNT/FACET).
func firstTable(t *testing.T, c *Client) string {
	t.Helper()
	info, err := c.Info()
	if err != nil {
		t.Fatalf("INFO: %v", err)
	}
	if len(info.Tables) == 0 {
		t.Skip("server has no tables; cannot exercise table-scoped commands")
	}
	return info.Tables[0]
}

// isProtocolError reports whether err is a server-side protocol/server error.
// Many v1.6 features (HIGHLIGHT, FACET, _score) require a verify_text-enabled
// table; on tables without it the server replies ERROR. Either outcome proves
// the client correctly round-trips the new clause.
func isProtocolError(err error) bool {
	var pe *ProtocolError
	var se *ServerError
	return errors.As(err, &pe) || errors.As(err, &se)
}

// -----------------------------------------------------------------------------
// connection
// -----------------------------------------------------------------------------

func TestE2EConnect(t *testing.T) {
	c := newE2EClient(t)
	if !c.IsConnected() {
		t.Fatal("IsConnected() = false after Connect")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c.IsConnected() {
		t.Fatal("IsConnected() = true after Close")
	}
}

func TestE2EConnectContext(t *testing.T) {
	ensureE2EServer(t)
	host, port := e2eAddr()
	c := NewClientWithConfig(ClientConfig{Host: host, Port: port, TimeoutMs: 5000, AdminToken: e2eAdminToken()})
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.ConnectContext(ctx); err != nil {
		t.Fatalf("ConnectContext: %v", err)
	}
}

// -----------------------------------------------------------------------------
// info / config / replication status
// -----------------------------------------------------------------------------

func TestE2EInfo(t *testing.T) {
	c := newE2EClient(t)
	info, err := c.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if !strings.HasPrefix(info.Version, "MygramDB") {
		t.Errorf("Version = %q, want prefix 'MygramDB'", info.Version)
	}
	if info.UptimeSeconds < 0 {
		t.Errorf("UptimeSeconds = %d, want >= 0", info.UptimeSeconds)
	}
	if info.ActiveConnections < 1 {
		t.Errorf("ActiveConnections = %d, want >= 1", info.ActiveConnections)
	}
	if info.Tables == nil {
		t.Error("Tables = nil, want non-nil slice")
	}
	// Readiness arrived in v1.10; an older server omits it, so only the
	// vocabulary is asserted. A server answering INFO with tables loaded is
	// serving, so "ready" is the expected verdict where the field exists.
	switch info.Readiness {
	case "":
	case "ready":
		if !info.Ready() {
			t.Error("Ready() = false for readiness 'ready'")
		}
	case "not_ready":
		t.Errorf("server reports readiness %q while answering INFO", info.Readiness)
	default:
		t.Errorf("unexpected readiness %q", info.Readiness)
	}
	// DataInitialized arrived with readiness, so it is only asserted where the
	// server reports the pair. A server that answers INFO with a ready verdict
	// has finished building its initial state by definition.
	if info.Readiness == "ready" && !info.DataInitialized {
		t.Error("DataInitialized = false while readiness is 'ready'")
	}
}

func TestE2EGetConfig(t *testing.T) {
	c := newE2EClient(t)
	cfg, err := c.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if !strings.Contains(cfg, "api:") || !strings.Contains(cfg, "port:") {
		t.Errorf("config missing expected keys; got:\n%s", cfg)
	}
}

func TestE2EReplicationStatus(t *testing.T) {
	c := newE2EClient(t)
	st, err := c.GetReplicationStatus()
	if err != nil {
		t.Fatalf("GetReplicationStatus: %v", err)
	}
	if st.StatusStr == "" {
		t.Error("StatusStr is empty")
	}
	// ProcessedEvents / QueueSize are only populated by the multi-line
	// response; do not require non-zero values.
	if st.ProcessedEvents < 0 || st.QueueSize < 0 {
		t.Errorf("negative replication counters: %+v", st)
	}
	// The diagnostics fields arrived in v1.10; an older server omits them and
	// leaves them zero-valued, so only their internal consistency is asserted.
	if st.CRCErrors < 0 {
		t.Errorf("CRCErrors = %d, want >= 0", st.CRCErrors)
	}
	if st.State != "" && st.Running != (st.State == "running") {
		t.Errorf("Running = %v contradicts State %q", st.Running, st.State)
	}
	// A stopped-on-error reader is the one case that carries a code, so a code
	// without a failure state would mean the two were read from different
	// fields.
	if st.LastErrorCode != ErrCodeNone && st.Running {
		t.Errorf("LastErrorCode = %d while replication is running", st.LastErrorCode)
	}
	// -1 is the server's "no event applied yet" sentinel, which is the normal
	// state of a freshly started server; any other negative value would mean
	// the lag was read from the wrong field.
	switch {
	case st.LastAppliedUnixtime == 0:
		if st.SecondsSinceLastApplied != 0 && st.SecondsSinceLastApplied != -1 {
			t.Errorf("SecondsSinceLastApplied = %d with no applied event, want 0 or -1",
				st.SecondsSinceLastApplied)
		}
	case st.SecondsSinceLastApplied < 0:
		t.Errorf("SecondsSinceLastApplied = %d after applying an event, want >= 0",
			st.SecondsSinceLastApplied)
	}
}

// -----------------------------------------------------------------------------
// administrative authentication (MygramDB >= v1.10)
//
// Only meaningful when the server under test configures api.admin_token; one
// started without it leaves the administrative commands ungated, so there is
// nothing to observe.
// -----------------------------------------------------------------------------

// newUnauthenticatedE2EClient connects deliberately without a token, to observe
// how the server gates a command from an unauthenticated connection.
func newUnauthenticatedE2EClient(t *testing.T) *Client {
	t.Helper()
	ensureE2EServer(t)
	host, port := e2eAddr()
	c := NewClientWithConfig(ClientConfig{Host: host, Port: port, TimeoutMs: 5000})
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func requireAdminToken(t *testing.T) string {
	t.Helper()
	token := e2eAdminToken()
	if token == "" {
		t.Skip("MYGRAM_ADMIN_TOKEN is unset; the server under test does not gate administrative commands")
	}
	return token
}

func TestE2EAuthSearchNeedsNoToken(t *testing.T) {
	requireAdminToken(t)
	table := firstTable(t, newE2EClient(t))
	c := newUnauthenticatedE2EClient(t)
	if _, err := c.Search(table, "python", SearchOptions{Limit: 1}); err != nil {
		t.Fatalf("Search on an unauthenticated connection: %v", err)
	}
}

func TestE2EAuthAdminCommandRefusedWithoutToken(t *testing.T) {
	requireAdminToken(t)
	c := newUnauthenticatedE2EClient(t)

	_, err := c.CacheStats()
	if err == nil {
		t.Fatal("CACHE STATS succeeded on an unauthenticated connection")
	}
	var se *ServerError
	if !errors.As(err, &se) {
		t.Fatalf("error = %T (%v), want *ServerError", err, err)
	}
	if se.Code != ErrCodePermissionDenied {
		t.Errorf("Code = %d, want %d (permission denied)", se.Code, ErrCodePermissionDenied)
	}
	if se.Retryable() {
		t.Error("Retryable() = true for a missing authorization")
	}
}

func TestE2EAuthUpgradesAnOpenConnection(t *testing.T) {
	token := requireAdminToken(t)
	c := newUnauthenticatedE2EClient(t)

	if err := c.Authenticate(token); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, err := c.CacheStats(); err != nil {
		t.Fatalf("CACHE STATS after Authenticate: %v", err)
	}
}

func TestE2EAuthWrongTokenFailsConnect(t *testing.T) {
	requireAdminToken(t)
	host, port := e2eAddr()
	c := NewClientWithConfig(ClientConfig{
		Host: host, Port: port, TimeoutMs: 5000, AdminToken: "wrong_token_e2e",
	})
	t.Cleanup(func() { _ = c.Close() })

	err := c.Connect()
	if err == nil {
		t.Fatal("Connect succeeded with a wrong admin token")
	}
	var se *ServerError
	if !errors.As(err, &se) {
		t.Fatalf("error = %T (%v), want *ServerError", err, err)
	}
	// A rejected AUTH must leave nothing half-open behind it.
	if c.IsConnected() {
		t.Error("IsConnected() = true after a rejected AUTH")
	}
}

func TestE2EAuthPooledConnections(t *testing.T) {
	token := requireAdminToken(t)
	host, port := e2eAddr()
	p := NewPool(PoolConfig{
		ClientConfig: ClientConfig{Host: host, Port: port, TimeoutMs: 15000, AdminToken: token},
		MaxOpenConns: 2,
	})
	t.Cleanup(func() { _ = p.Close() })

	// An administrative command over a pooled connection only succeeds if that
	// connection authenticated as it was opened; the pool never sees AUTH.
	for i := range 2 {
		if _, err := p.CacheStats(); err != nil {
			t.Fatalf("CACHE STATS over pooled connection %d: %v", i, err)
		}
	}
}

// -----------------------------------------------------------------------------
// typed server errors
// -----------------------------------------------------------------------------

func TestE2EServerErrorCode(t *testing.T) {
	c := newE2EClient(t)

	_, err := c.Search("no_such_table_e2e", "python", SearchOptions{})
	if err == nil {
		t.Fatal("Search against an unknown table succeeded")
	}
	var se *ServerError
	if !errors.As(err, &se) {
		t.Fatalf("error = %T (%v), want *ServerError", err, err)
	}
	// A pre-v1.10 server sends the same rejection without a code, which is
	// ErrCodeNone rather than a wrong code.
	switch se.Code {
	case ErrCodeNone:
		t.Logf("server sent an uncoded ERROR frame: %v", se)
	case ErrCodeTableNotFound:
		// The code is carried in the field, not left in the message.
		if strings.HasPrefix(se.Message, strconv.Itoa(int(ErrCodeTableNotFound))) {
			t.Errorf("Message still carries the numeric code: %q", se.Message)
		}
		if se.Retryable() {
			t.Error("Retryable() = true for a missing table")
		}
	default:
		t.Errorf("Code = %d, want %d (table not found)", se.Code, ErrCodeTableNotFound)
	}
}

// -----------------------------------------------------------------------------
// debug mode
// -----------------------------------------------------------------------------

// TestE2EDebugRawCommand turns DEBUG on through SendCommand rather than
// EnableDebug: the client must follow the acknowledgement so the next search
// reply is framed with its debug block.
func TestE2EDebugRawCommand(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	if _, err := c.SendCommand("debug on"); err != nil {
		t.Fatalf("DEBUG ON: %v", err)
	}
	resp, err := c.Search(table, "test", SearchOptions{Limit: 1})
	if err != nil {
		t.Fatalf("Search under DEBUG: %v", err)
	}
	if resp.Debug == nil {
		t.Error("Debug = nil, want the DEBUG block of the reply")
	}
	if _, err := c.SendCommand("DEBUG OFF"); err != nil {
		t.Fatalf("DEBUG OFF: %v", err)
	}
	resp, err = c.Search(table, "test", SearchOptions{Limit: 1})
	if err != nil {
		t.Fatalf("Search after DEBUG OFF: %v", err)
	}
	if resp.Debug != nil {
		t.Errorf("Debug = %+v after DEBUG OFF, want nil", resp.Debug)
	}
}

// TestE2ESearchTermNamedHighlight searches for the word itself: it is data,
// not the HIGHLIGHT clause, and the reply must not be awaited as highlight rows.
func TestE2ESearchTermNamedHighlight(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	if _, err := c.Search(table, "highlight", SearchOptions{Limit: 1}); err != nil {
		t.Fatalf("Search \"highlight\": %v", err)
	}
}

func TestE2EDebugMode(t *testing.T) {
	c := newE2EClient(t)
	if err := c.EnableDebug(); err != nil {
		t.Fatalf("EnableDebug: %v", err)
	}
	if err := c.DisableDebug(); err != nil {
		t.Fatalf("DisableDebug: %v", err)
	}
}

// -----------------------------------------------------------------------------
// search
// -----------------------------------------------------------------------------

func TestE2ESearchBasic(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	resp, err := c.Search(table, "test", SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.TotalCount < 0 {
		t.Errorf("TotalCount = %d, want >= 0", resp.TotalCount)
	}
	if resp.Results == nil {
		t.Error("Results = nil")
	}
}

func TestE2ESearchLimit(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	resp, err := c.Search(table, "test", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) > 5 {
		t.Errorf("len(Results) = %d, want <= 5", len(resp.Results))
	}
}

func TestE2ESearchOffset(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	if _, err := c.Search(table, "test", SearchOptions{Limit: 10, Offset: 5}); err != nil {
		t.Fatalf("Search with offset: %v", err)
	}
	// Offset>0 with Limit==0 must emit a bare OFFSET clause (regression for 3e23e9c).
	if _, err := c.Search(table, "test", SearchOptions{Offset: 5}); err != nil {
		t.Fatalf("Search with bare offset: %v", err)
	}
}

func TestE2ESearchAndNot(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	if _, err := c.Search(table, "hello", SearchOptions{AndTerms: []string{"world"}}); err != nil {
		t.Fatalf("Search AND: %v", err)
	}
	if _, err := c.Search(table, "hello", SearchOptions{NotTerms: []string{"goodbye"}}); err != nil {
		t.Fatalf("Search NOT: %v", err)
	}
}

func TestE2ESearchDebug(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	if err := c.EnableDebug(); err != nil {
		t.Fatalf("EnableDebug: %v", err)
	}
	t.Cleanup(func() { _ = c.DisableDebug() })

	resp, err := c.Search(table, "test", SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Debug == nil {
		t.Fatal("Debug = nil with debug mode enabled")
	}
	if resp.Debug.QueryTimeMs < 0 {
		t.Errorf("QueryTimeMs = %v, want >= 0", resp.Debug.QueryTimeMs)
	}
}

// -----------------------------------------------------------------------------
// v1.6 fuzzy / highlight / _score / facet
// -----------------------------------------------------------------------------

func TestE2EFuzzy(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	for _, dist := range []int{1, 2} {
		resp, err := c.Search(table, "machne", SearchOptions{Fuzzy: dist, Limit: 5})
		if err != nil {
			t.Fatalf("Search Fuzzy=%d: %v", dist, err)
		}
		if resp.Results == nil {
			t.Errorf("Fuzzy=%d: Results = nil", dist)
		}
	}
}

func TestE2EHighlight(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	resp, err := c.Search(table, "test", SearchOptions{Highlight: &HighlightOptions{}, Limit: 3})
	if err != nil {
		// HIGHLIGHT requires verify_text=ascii|all; tables without it
		// reply ERROR. Either outcome proves the clause round-trips.
		if !isProtocolError(err) {
			t.Fatalf("Search Highlight: unexpected error type %T: %v", err, err)
		}
		return
	}
	for _, r := range resp.Results {
		_ = r.Snippet // any string (including "") is acceptable
	}
}

func TestE2EHighlightCustom(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	resp, err := c.Search(table, "test", SearchOptions{
		Highlight: &HighlightOptions{
			OpenTag:      "<mark>",
			CloseTag:     "</mark>",
			SnippetLen:   80,
			MaxFragments: 2,
		},
		Limit: 3,
	})
	if err != nil {
		if !isProtocolError(err) {
			t.Fatalf("Search Highlight (custom): %v", err)
		}
		return
	}
	if resp.Results == nil {
		t.Error("Results = nil")
	}
}

func TestE2ESortByScore(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	_, err := c.Search(table, "test", SearchOptions{
		SortColumn: "_score",
		SortDesc:   true,
		Limit:      5,
	})
	if err != nil && !isProtocolError(err) {
		t.Fatalf("Search SORT _score: %v", err)
	}
}

func TestE2EFacet(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)

	// Bare FACET — server may reject if no filter columns exist.
	if _, err := c.Facet(table, "status", FacetOptions{}); err != nil && !isProtocolError(err) {
		t.Fatalf("Facet bare: %v", err)
	}

	// Scoped FACET (QUERY + LIMIT).
	if _, err := c.Facet(table, "status", FacetOptions{Query: "test", Limit: 5}); err != nil && !isProtocolError(err) {
		t.Fatalf("Facet scoped: %v", err)
	}
}

// -----------------------------------------------------------------------------
// count
// -----------------------------------------------------------------------------

func TestE2ECountBasic(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	resp, err := c.Count(table, "test", CountOptions{})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if resp.Count < 0 {
		t.Errorf("Count = %d, want >= 0", resp.Count)
	}
}

func TestE2ECountAndNot(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	if _, err := c.Count(table, "hello", CountOptions{AndTerms: []string{"world"}}); err != nil {
		t.Fatalf("Count AND: %v", err)
	}
	if _, err := c.Count(table, "hello", CountOptions{NotTerms: []string{"goodbye"}}); err != nil {
		t.Fatalf("Count NOT: %v", err)
	}
}

func TestE2ECountDebug(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	if err := c.EnableDebug(); err != nil {
		t.Fatalf("EnableDebug: %v", err)
	}
	t.Cleanup(func() { _ = c.DisableDebug() })

	resp, err := c.Count(table, "test", CountOptions{})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if resp.Debug == nil {
		t.Fatal("Debug = nil with debug mode enabled")
	}
}

// -----------------------------------------------------------------------------
// search expression simplification
// -----------------------------------------------------------------------------

func TestE2ESimplifyAndExecute(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	cases := []struct {
		name string
		expr string
	}{
		{"simple AND", "hello world"},
		{"required", "+golang +tutorial"},
		{"excluded", "+programming -java"},
		{"phrase", `"machine learning" tutorial`},
		{"japanese full-width space", "機械学習　チュートリアル"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := SimplifySearchExpression(tc.expr)
			if err != nil {
				t.Fatalf("SimplifySearchExpression: %v", err)
			}
			if _, err := c.Search(table, s.MainTerm, SearchOptions{
				AndTerms: s.AndTerms,
				NotTerms: s.NotTerms,
				Limit:    5,
			}); err != nil {
				t.Fatalf("Search: %v", err)
			}
		})
	}
}

func TestE2ESimplifyCount(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	s, err := SimplifySearchExpression("+hello +world -goodbye")
	if err != nil {
		t.Fatalf("SimplifySearchExpression: %v", err)
	}
	if _, err := c.Count(table, s.MainTerm, CountOptions{
		AndTerms: s.AndTerms,
		NotTerms: s.NotTerms,
	}); err != nil {
		t.Fatalf("Count: %v", err)
	}
}

// -----------------------------------------------------------------------------
// cache
// -----------------------------------------------------------------------------

func TestE2ECacheStats(t *testing.T) {
	c := newE2EClient(t)
	st, err := c.CacheStats()
	if err != nil {
		t.Fatalf("CacheStats: %v", err)
	}
	if st.Hits < 0 || st.Misses < 0 {
		t.Errorf("negative cache counters: %+v", st)
	}
	if st.TotalQueries != st.Hits+st.Misses {
		t.Errorf("total_queries = %d, want hits+misses = %d", st.TotalQueries, st.Hits+st.Misses)
	}
	// The accounted total covers the payloads plus both invalidation structures,
	// so it can never be the smaller number.
	if st.AccountedMemoryBytes < st.MemoryBytes {
		t.Errorf("accounted_memory_bytes (%d) < current_memory_bytes (%d)", st.AccountedMemoryBytes, st.MemoryBytes)
	}
	// The four rejection counters are independent reasons, so each is only
	// required to be non-negative and parsed rather than left at zero by a
	// key the parser does not know.
	for name, v := range map[string]int{
		"rejection_count":         st.RejectionCount,
		"rejection_oversize":      st.RejectionOversize,
		"rejection_memory_budget": st.RejectionMemoryBudget,
		"rejection_duplicate":     st.RejectionDuplicate,
		"invalidations_immediate": st.InvalidationsImmediate,
		"invalidations_deferred":  st.InvalidationsDeferred,
		"invalidations_batches":   st.InvalidationsBatches,
	} {
		if v < 0 {
			t.Errorf("%s = %d, want >= 0", name, v)
		}
	}
}

func TestE2EDumpSaveAndWait(t *testing.T) {
	if os.Getenv("MYGRAM_E2E_SKIP_DUMP") == "1" {
		t.Skip("MYGRAM_E2E_SKIP_DUMP=1")
	}
	c := newE2EClient(t)

	name := fmt.Sprintf("e2e_wait_%d.dump", time.Now().UnixNano())
	path, err := c.DumpSaveAndWait(name)
	if err != nil {
		if isProtocolError(err) {
			t.Skipf("DUMP SAVE rejected by server (likely disabled or dump_dir unwritable): %v", err)
		}
		t.Fatalf("DumpSaveAndWait: %v", err)
	}
	if path == "" {
		t.Fatal("DumpSaveAndWait returned an empty path")
	}
	// The wait resolved the dump, so nothing may still be running behind it.
	st, err := c.DumpStatus()
	if err != nil {
		t.Fatalf("DumpStatus: %v", err)
	}
	if st.SaveInProgress {
		t.Errorf("DumpSaveAndWait returned %q while a save is still in progress: %+v", path, st)
	}
}

func TestE2ECacheClear(t *testing.T) {
	c := newE2EClient(t)
	if err := c.CacheClear(""); err != nil {
		t.Fatalf("CacheClear: %v", err)
	}
}

func TestE2ECacheToggle(t *testing.T) {
	c := newE2EClient(t)
	before, err := c.CacheStats()
	if err != nil {
		t.Fatalf("CacheStats (before): %v", err)
	}

	// Restore original state on exit.
	t.Cleanup(func() {
		if before.Enabled {
			_ = c.CacheEnable()
		} else {
			_ = c.CacheDisable()
		}
	})

	if before.Enabled {
		if err := c.CacheDisable(); err != nil {
			t.Fatalf("CacheDisable: %v", err)
		}
	} else {
		if err := c.CacheEnable(); err != nil {
			t.Fatalf("CacheEnable: %v", err)
		}
	}

	after, err := c.CacheStats()
	if err != nil {
		t.Fatalf("CacheStats (after): %v", err)
	}
	if after.Enabled == before.Enabled {
		t.Errorf("Enabled did not toggle: before=%v after=%v", before.Enabled, after.Enabled)
	}
}

// -----------------------------------------------------------------------------
// optimize
// -----------------------------------------------------------------------------

func TestE2EOptimize(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	if err := c.Optimize(table); err != nil {
		t.Fatalf("Optimize: %v", err)
	}
}

// -----------------------------------------------------------------------------
// dump
// -----------------------------------------------------------------------------

func TestE2EDumpStatus(t *testing.T) {
	c := newE2EClient(t)
	st, err := c.DumpStatus()
	if err != nil {
		t.Fatalf("DumpStatus: %v", err)
	}
	if st.TablesProcessed < 0 || st.TablesTotal < 0 {
		t.Errorf("negative dump counters: %+v", st)
	}
}

// TestE2EDumpRoundtrip exercises DUMP SAVE -> DUMP STATUS poll -> DUMP VERIFY
// -> DUMP INFO -> DUMP LOAD against a server-managed file. The dump filename
// is randomized to avoid clashes between concurrent test runs. Skipped via
// MYGRAM_E2E_SKIP_DUMP=1 when the server's dump_dir is read-only.
func TestE2EDumpRoundtrip(t *testing.T) {
	if os.Getenv("MYGRAM_E2E_SKIP_DUMP") == "1" {
		t.Skip("MYGRAM_E2E_SKIP_DUMP=1")
	}
	c := newE2EClient(t)

	dumpName := fmt.Sprintf("e2e_test_%d.dump", time.Now().UnixNano())

	// SAVE is asynchronous; the server returns once the request is queued.
	if _, err := c.DumpSave(dumpName); err != nil {
		// DUMP commands may be disabled or the dir may not be writable;
		// surface a clear skip rather than a hard failure.
		if isProtocolError(err) {
			t.Skipf("DUMP SAVE rejected by server (likely disabled or dump_dir unwritable): %v", err)
		}
		t.Fatalf("DumpSave: %v", err)
	}

	// Poll until the save completes (bounded).
	deadline := time.Now().Add(20 * time.Second)
	var st *DumpStatus
	for time.Now().Before(deadline) {
		var err error
		st, err = c.DumpStatus()
		if err != nil {
			t.Fatalf("DumpStatus poll: %v", err)
		}
		if !st.SaveInProgress {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if st == nil || st.SaveInProgress {
		t.Fatalf("DUMP SAVE did not complete within 20s: %+v", st)
	}
	if st.Error != "" {
		t.Fatalf("DUMP SAVE reported error: %s", st.Error)
	}

	// Resolve the produced file path from the latest status; fall back to
	// the requested filename if the server didn't echo it back.
	dumpPath := st.ResultFilepath
	if dumpPath == "" {
		dumpPath = dumpName
	}

	if _, err := c.DumpVerify(dumpPath); err != nil {
		t.Errorf("DumpVerify: %v", err)
	}
	if _, err := c.DumpInfo(dumpPath); err != nil {
		t.Errorf("DumpInfo: %v", err)
	}

	// LOAD the just-saved file. This is effectively a no-op data-wise
	// (same snapshot reapplied) but exercises the LOAD path.
	if err := c.DumpLoad(dumpPath); err != nil {
		// Some configurations disallow runtime LOAD; tolerate that.
		if !isProtocolError(err) {
			t.Errorf("DumpLoad: %v", err)
		}
	}
}

// -----------------------------------------------------------------------------
// replication start/stop
// -----------------------------------------------------------------------------

// TestE2EReplicationToggle stops then starts (or vice versa) replication and
// verifies the Running flag flipped, then restores the original state.
// Skipped when replication is unavailable (e.g. server compiled without
// MySQL support or mysql config absent), surfaced as a ProtocolError.
func TestE2EReplicationToggle(t *testing.T) {
	if os.Getenv("MYGRAM_E2E_SKIP_REPLICATION") == "1" {
		t.Skip("MYGRAM_E2E_SKIP_REPLICATION=1")
	}
	c := newE2EClient(t)

	before, err := c.GetReplicationStatus()
	if err != nil {
		t.Fatalf("GetReplicationStatus (before): %v", err)
	}

	t.Cleanup(func() {
		// Best-effort restore.
		after, err := c.GetReplicationStatus()
		if err != nil {
			return
		}
		switch {
		case before.Running && !after.Running:
			_ = c.StartReplication()
		case !before.Running && after.Running:
			_ = c.StopReplication()
		}
	})

	toggle := func() error {
		if before.Running {
			return c.StopReplication()
		}
		return c.StartReplication()
	}

	if err := toggle(); err != nil {
		if isProtocolError(err) {
			t.Skipf("replication not available on this server: %v", err)
		}
		t.Fatalf("toggle replication: %v", err)
	}

	// Allow the server a brief moment to reflect the new state.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, err := c.GetReplicationStatus()
		if err != nil {
			t.Fatalf("GetReplicationStatus (after): %v", err)
		}
		if st.Running != before.Running {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("replication Running flag did not change from %v within 2s", before.Running)
}

// -----------------------------------------------------------------------------
// v1.7 protocol surfaces (version-agnostic round-trips)
// -----------------------------------------------------------------------------

func TestE2ESearchRaw(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)
	// Boolean expressions are sent verbatim (v1.8 unquoted transport). Tolerate
	// servers that reject the clause so the test proves the client round-trips
	// the expression whole. Include an OR group nested under AND — the shape
	// v1.8 fixed — to exercise the parser end to end.
	exprs := []string{
		"test OR example",
		"test AND (example OR sample)",
	}
	for _, expr := range exprs {
		if _, err := c.SearchRaw(table, expr, SearchRawOptions{Limit: 5}); err != nil && !isProtocolError(err) {
			t.Fatalf("SearchRaw %q: %v", expr, err)
		}
	}
}

func TestE2EShowVariables(t *testing.T) {
	c := newE2EClient(t)
	out, err := c.ShowVariables("")
	if err != nil {
		if isProtocolError(err) {
			t.Skipf("SHOW VARIABLES not supported on this server: %v", err)
		}
		t.Fatalf("ShowVariables: %v", err)
	}
	if out == "" {
		t.Error("ShowVariables returned empty response")
	}

	// Pattern variant should also round-trip.
	if _, err := c.ShowVariables("logging%"); err != nil && !isProtocolError(err) {
		t.Fatalf("ShowVariables LIKE: %v", err)
	}
}

func TestE2ESetVariable(t *testing.T) {
	c := newE2EClient(t)
	// logging.level is a commonly settable runtime variable; tolerate servers
	// that do not expose it.
	if err := c.SetVariable("logging.level", "info"); err != nil && !isProtocolError(err) {
		t.Fatalf("SetVariable: %v", err)
	}
}

func TestE2ESyncFamily(t *testing.T) {
	c := newE2EClient(t)
	table := firstTable(t, c)

	// SYNC STATUS is a read-only probe and should always frame correctly.
	if _, err := c.SyncStatus(); err != nil && !isProtocolError(err) {
		t.Fatalf("SyncStatus: %v", err)
	}

	// SYNC / SYNC STOP mutate server state; tolerate protocol errors but make
	// sure we stop any sync we may have started.
	if _, err := c.Sync(table); err != nil && !isProtocolError(err) {
		t.Fatalf("Sync: %v", err)
	}
	t.Cleanup(func() { _, _ = c.SyncStop(table) })

	// SYNC is asynchronous: the server acknowledges at once and rebuilds the
	// table in the background, rejecting queries against it with
	// ErrCodeServerNotReady until it finishes. Leaving that in flight would
	// fail whichever test runs next, so the test that starts a resync is the
	// one that waits for it.
	waitForSyncIdle(t, c, table, 30*time.Second)
}

// syncInFlightStates are the SYNC STATUS values that mean a resync is still
// running. Terminal states (COMPLETED, FAILED) and any state this client does
// not know are treated as settled, so an unfamiliar report cannot stall the
// suite — a table that is in fact still syncing surfaces as a failure in the
// test that queries it, which is louder than a timeout here.
var syncInFlightStates = map[string]bool{
	"STARTING":    true,
	"IN_PROGRESS": true,
	"RUNNING":     true,
	"PENDING":     true,
}

// waitForSyncIdle polls SYNC STATUS until table reports no in-flight resync.
func waitForSyncIdle(t *testing.T, c *Client, table string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		report, err := c.SyncStatus()
		if err != nil {
			// A server that does not report sync status has nothing to wait on.
			if isProtocolError(err) {
				return
			}
			t.Fatalf("SyncStatus: %v", err)
		}
		if !syncInFlight(report, table) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("SYNC of %q did not settle within %s; last report:\n%s", table, timeout, report)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// syncInFlight reports whether a SYNC_STATUS report shows table mid-resync.
//
// A report line reads "table=<db>.<name> status=<state> ...". The table is
// matched on its unqualified name, since SYNC accepts a bare name while the
// report always answers with the qualified one.
func syncInFlight(report, table string) bool {
	want := bareTableName(table)
	for _, line := range strings.Split(strings.ReplaceAll(report, "\r\n", "\n"), "\n") {
		var reported, state string
		for _, f := range strings.Fields(line) {
			switch {
			case strings.HasPrefix(f, "table="):
				reported = bareTableName(strings.TrimPrefix(f, "table="))
			case strings.HasPrefix(f, "status="):
				state = strings.TrimPrefix(f, "status=")
			}
		}
		if reported == want && syncInFlightStates[state] {
			return true
		}
	}
	return false
}

// bareTableName strips a database qualifier so a bare name compares equal to
// the qualified one the server reports back.
func bareTableName(identity string) string {
	if _, table, err := ParseTableIdentity(identity); err == nil {
		return table
	}
	return identity
}

// -----------------------------------------------------------------------------
// seeded dataset (docker e2e) — gated by MYGRAM_E2E_SEEDED=1
//
// These assertions require the deterministic dataset created by the
// docker-compose harness under e2e/docker/. Without the seed they would fail
// against an arbitrary server, so they only run when MYGRAM_E2E_SEEDED=1.
// -----------------------------------------------------------------------------

// collectPKs runs a search and returns the matched primary keys as a set.
func collectPKs(t *testing.T, c *Client, table, query string, opts SearchOptions) map[string]bool {
	t.Helper()
	resp, err := c.Search(table, query, opts)
	if err != nil {
		t.Fatalf("Search %q: %v", query, err)
	}
	set := make(map[string]bool, len(resp.Results))
	for _, r := range resp.Results {
		set[r.PrimaryKey] = true
	}
	return set
}

func assertPKSet(t *testing.T, got map[string]bool, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("primary key set = %v, want %v", keysOf(got), want)
		return
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("primary key set %v missing %q", keysOf(got), w)
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestE2ESeeded(t *testing.T) {
	if os.Getenv("MYGRAM_E2E_SEEDED") != "1" {
		t.Skip("MYGRAM_E2E_SEEDED != 1; run via e2e/docker harness")
	}
	c := newE2EClient(t)
	const table = "articles"

	t.Run("bare and qualified identity resolve identically", func(t *testing.T) {
		bare := collectPKs(t, c, table, "python", SearchOptions{})
		qualified := collectPKs(t, c, "testdb.articles", "python", SearchOptions{})
		assertPKSet(t, bare, "3")
		assertPKSet(t, qualified, "3")
	})

	t.Run("phrase search with required filter", func(t *testing.T) {
		// id 6 ("machine learning hidden disabled") is enabled=0 and hidden.
		got := collectPKs(t, c, table, "machine learning", SearchOptions{})
		assertPKSet(t, got, "3")
	})

	t.Run("searchRaw boolean OR", func(t *testing.T) {
		resp, err := c.SearchRaw(table, "ruby OR python", SearchRawOptions{Limit: 10})
		if err != nil {
			t.Fatalf("SearchRaw: %v", err)
		}
		got := make(map[string]bool, len(resp.Results))
		for _, r := range resp.Results {
			got[r.PrimaryKey] = true
		}
		assertPKSet(t, got, "2", "3")
	})

	t.Run("searchRaw OR group nested under AND", func(t *testing.T) {
		// v1.8 unquoted transport + nested-OR parsing. id 2 ("ruby on rails
		// programming guide") matches "programming" AND "ruby"; id 3 has
		// "python" but not "programming", so the set is exactly {2}.
		resp, err := c.SearchRaw(table, "programming AND (ruby OR python)", SearchRawOptions{Limit: 10})
		if err != nil {
			t.Fatalf("SearchRaw: %v", err)
		}
		got := make(map[string]bool, len(resp.Results))
		for _, r := range resp.Results {
			got[r.PrimaryKey] = true
		}
		assertPKSet(t, got, "2")
	})

	// Web-syntax expressions are converted client-side and sent as boolean
	// text, so the server's grammar decides what each conversion means.
	rawPKs := func(t *testing.T, expression string) map[string]bool {
		t.Helper()
		query, err := ConvertSearchExpression(expression)
		if err != nil {
			t.Fatalf("ConvertSearchExpression(%q): %v", expression, err)
		}
		resp, err := c.SearchRaw(table, query, SearchRawOptions{Limit: 10})
		if err != nil {
			t.Fatalf("SearchRaw(%q): %v", query, err)
		}
		got := make(map[string]bool, len(resp.Results))
		for _, r := range resp.Results {
			got[r.PrimaryKey] = true
		}
		return got
	}

	t.Run("expression OR binds looser than implicit AND", func(t *testing.T) {
		// "ruby OR python basics" is "ruby OR (python basics)": id 2 has ruby,
		// id 3 has python and basics. Read left to right it would be {3}.
		assertPKSet(t, rawPKs(t, "ruby OR python basics"), "2", "3")
	})

	t.Run("expression negated group excludes the whole group", func(t *testing.T) {
		// id 3 has "machine" but not "rails", so it survives NOT (machine rails).
		assertPKSet(t, rawPKs(t, "python -(machine rails)"), "3")
	})

	t.Run("expression term starting with OR is not the operator", func(t *testing.T) {
		assertPKSet(t, rawPKs(t, "python OR-tools"))
	})

	t.Run("expression quoted phrase shaped like a group is literal", func(t *testing.T) {
		if got := rawPKs(t, `"(python)"`); got["3"] {
			t.Errorf("literal \"(python)\" matched %v; a quoted phrase must not become a group", keysOf(got))
		}
	})

	t.Run("japanese ngram match", func(t *testing.T) {
		got := collectPKs(t, c, table, "機械学習", SearchOptions{})
		assertPKSet(t, got, "1", "5")
	})

	t.Run("count golang", func(t *testing.T) {
		resp, err := c.Count(table, "golang", CountOptions{})
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if resp.Count != 1 {
			t.Errorf("Count golang = %d, want 1", resp.Count)
		}
	})

	t.Run("facet by category", func(t *testing.T) {
		resp, err := c.Facet(table, "category", FacetOptions{})
		if err != nil {
			t.Fatalf("Facet: %v", err)
		}
		counts := make(map[string]int64, len(resp.Results))
		for _, v := range resp.Results {
			counts[v.Value] = v.Count
		}
		if counts["tech"] != 3 {
			t.Errorf("facet tech = %d, want 3", counts["tech"])
		}
		if counts["science"] != 2 {
			t.Errorf("facet science = %d, want 2", counts["science"])
		}
	})

	t.Run("facet pagination reports the distinct total", func(t *testing.T) {
		full, err := c.Facet(table, "category", FacetOptions{})
		if err != nil {
			t.Fatalf("Facet: %v", err)
		}
		if full.TotalCount != int64(len(full.Results)) {
			t.Errorf("unpaginated TotalCount = %d, want %d", full.TotalCount, len(full.Results))
		}
		if full.TotalCount < 2 {
			t.Fatalf("seed should expose at least two categories, got %d", full.TotalCount)
		}

		page, err := c.Facet(table, "category", FacetOptions{Limit: 1, Offset: 1})
		if err != nil {
			t.Fatalf("paged Facet: %v", err)
		}
		if len(page.Results) != 1 {
			t.Errorf("page holds %d values, want 1", len(page.Results))
		}
		if page.TotalCount != full.TotalCount {
			t.Errorf("paged TotalCount = %d, want %d", page.TotalCount, full.TotalCount)
		}
		if len(full.Results) > 1 && page.Results[0].Value != full.Results[1].Value {
			t.Errorf("OFFSET 1 returned %q, want %q", page.Results[0].Value, full.Results[1].Value)
		}
	})

	t.Run("primary key ordering", func(t *testing.T) {
		// A boolean expression selects three visible rows whose primary keys
		// are 2, 3 and 4, so the two orderings are exact reversals.
		const expr = "ruby OR python OR golang"
		order := func(sort PrimaryKeySort) []string {
			t.Helper()
			resp, err := c.Search(table, expr, SearchOptions{
				QueryMode:      QueryBoolean,
				SortPrimaryKey: sort,
				Limit:          10,
			})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			out := make([]string, 0, len(resp.Results))
			for _, r := range resp.Results {
				out = append(out, r.PrimaryKey)
			}
			return out
		}

		asc := order(PrimaryKeySortAsc)
		if !reflect.DeepEqual(asc, []string{"2", "3", "4"}) {
			t.Errorf("ascending order = %v, want [2 3 4]", asc)
		}
		desc := order(PrimaryKeySortDesc)
		if !reflect.DeepEqual(desc, []string{"4", "3", "2"}) {
			t.Errorf("descending order = %v, want [4 3 2]", desc)
		}
		// Omitting the clause leaves the server on its own default, which is
		// the same descending order the explicit form asks for.
		if def := order(PrimaryKeySortDefault); !reflect.DeepEqual(def, desc) {
			t.Errorf("default order = %v, want the descending order %v", def, desc)
		}
	})

	t.Run("comparison filters bound status", func(t *testing.T) {
		// Exactly one visible row carries the ASCII text "learning": id 3
		// ("python machine learning basics", status 2). id 6 has the same words
		// but enabled = 0, and id 5 is Japanese. Each operator is checked on
		// both sides of that known value.
		cases := []struct {
			name  string
			op    FilterOp
			value string
			want  int
		}{
			{"equal matches", FilterEqual, "2", 1},
			{"equal excludes", FilterEqual, "1", 0},
			{"not equal excludes", FilterNotEqual, "2", 0},
			{"not equal matches", FilterNotEqual, "1", 1},
			{"greater than excludes", FilterGreaterThan, "2", 0},
			{"greater than matches", FilterGreaterThan, "1", 1},
			{"greater or equal matches", FilterGreaterThanOrEqual, "2", 1},
			{"greater or equal excludes", FilterGreaterThanOrEqual, "3", 0},
			{"less than excludes", FilterLessThan, "2", 0},
			{"less than matches", FilterLessThan, "3", 1},
			{"less or equal matches", FilterLessThanOrEqual, "2", 1},
			{"less or equal excludes", FilterLessThanOrEqual, "1", 0},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				resp, err := c.Count(table, "learning", CountOptions{
					FilterConditions: []FilterCondition{{Key: "status", Op: tc.op, Value: tc.value}},
				})
				if err != nil {
					t.Fatalf("Count with comparison filter: %v", err)
				}
				if resp.Count != tc.want {
					t.Errorf("Count = %d, want %d", resp.Count, tc.want)
				}
			})
		}
	})

	t.Run("boolean query mode combines with a sort", func(t *testing.T) {
		resp, err := c.Search(table, "ruby OR python", SearchOptions{
			QueryMode:  QueryBoolean,
			SortColumn: "id",
			SortDesc:   true,
			Limit:      10,
		})
		if err != nil {
			t.Fatalf("boolean Search: %v", err)
		}
		got := make(map[string]bool, len(resp.Results))
		for _, r := range resp.Results {
			got[r.PrimaryKey] = true
		}
		assertPKSet(t, got, "2", "3")
	})

	t.Run("searchWithHighlights wraps snippet", func(t *testing.T) {
		resp, err := c.SearchWithHighlights(table, "python", SearchOptions{})
		if err != nil {
			if isProtocolError(err) {
				t.Skipf("highlight not available: %v", err)
			}
			t.Fatalf("SearchWithHighlights: %v", err)
		}
		if len(resp.Results) == 0 {
			t.Fatal("no results for highlighted python search")
		}
		if !strings.Contains(resp.Results[0].Snippet, "python") {
			t.Errorf("snippet %q does not contain matched term", resp.Results[0].Snippet)
		}
	})
}
