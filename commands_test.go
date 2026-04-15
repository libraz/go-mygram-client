package mygram

import (
	"strings"
	"testing"
)

// Tests for pre-v1.6 commands that previously had 0% coverage. Response
// shapes below are drawn from the MygramDB server handlers under
// src/server/handlers/ (cache_handler.cpp, dump_handler.cpp,
// debug_handler.cpp, response_formatter.cpp).

// --- REPLICATION -------------------------------------------------------------

func TestGetReplicationStatus(t *testing.T) {
	response := "OK REPLICATION\r\n" +
		"status: running\r\n" +
		"current_gtid: 3E11FA47-71CA-11E1-9E33-C80AA9429562:1-100\r\n" +
		"processed_events: 42\r\n" +
		"queue_size: 3\r\n" +
		"END\r\n"
	srv, addr, cleanup := newRecordingServer(t, response)
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	status, err := c.GetReplicationStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running {
		t.Error("Running should be true")
	}
	if !strings.Contains(status.GTID, "3E11FA47") {
		t.Errorf("GTID = %q", status.GTID)
	}

	if got := srv.lastCommand(); got != "REPLICATION STATUS" {
		t.Errorf("command = %q, want %q", got, "REPLICATION STATUS")
	}
}

func TestStartReplication(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK REPLICATION_STARTED\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := c.StartReplication(); err != nil {
		t.Fatal(err)
	}
	if got := srv.lastCommand(); got != "REPLICATION START" {
		t.Errorf("command = %q, want %q", got, "REPLICATION START")
	}
}

func TestStopReplication(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK REPLICATION_STOPPED\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := c.StopReplication(); err != nil {
		t.Fatal(err)
	}
	if got := srv.lastCommand(); got != "REPLICATION STOP" {
		t.Errorf("command = %q, want %q", got, "REPLICATION STOP")
	}
}

func TestStartReplicationServerError(t *testing.T) {
	_, addr, cleanup := newRecordingServer(t, "ERROR replication not configured\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	err := c.StartReplication()
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*ServerError); !ok {
		t.Errorf("expected ServerError, got %T: %v", err, err)
	}
}

// --- DEBUG -------------------------------------------------------------------

func TestEnableDebug(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK DEBUG_ON\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := c.EnableDebug(); err != nil {
		t.Fatal(err)
	}
	if got := srv.lastCommand(); got != "DEBUG ON" {
		t.Errorf("command = %q, want %q", got, "DEBUG ON")
	}
}

func TestDisableDebug(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK DEBUG_OFF\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := c.DisableDebug(); err != nil {
		t.Fatal(err)
	}
	if got := srv.lastCommand(); got != "DEBUG OFF" {
		t.Errorf("command = %q, want %q", got, "DEBUG OFF")
	}
}

// --- CONFIG ------------------------------------------------------------------

func TestGetConfig(t *testing.T) {
	// CONFIG multi-line response. Client returns the raw (trimmed) payload.
	response := "OK CONFIG\r\n" +
		"  mysql:\r\n" +
		"    host: 127.0.0.1\r\n" +
		"  tables: 1\r\n" +
		"\r\n"
	srv, addr, cleanup := newRecordingServer(t, response)
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	got, err := c.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "OK CONFIG") {
		t.Errorf("GetConfig = %q, want prefix %q", got, "OK CONFIG")
	}
	if !strings.Contains(got, "host: 127.0.0.1") {
		t.Errorf("GetConfig missing host line: %q", got)
	}
	if cmd := srv.lastCommand(); cmd != "CONFIG" {
		t.Errorf("command = %q, want %q", cmd, "CONFIG")
	}
}

// --- DUMP --------------------------------------------------------------------

func TestDumpSaveWithFilepath(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK SAVED /tmp/dump.bin\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	resp, err := c.DumpSave("/tmp/dump.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp, "/tmp/dump.bin") {
		t.Errorf("response = %q, want filepath", resp)
	}
	if cmd := srv.lastCommand(); cmd != "DUMP SAVE /tmp/dump.bin" {
		t.Errorf("command = %q", cmd)
	}
}

func TestDumpSaveNoFilepath(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK SAVED /var/lib/mygramdb/auto.dmp\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if _, err := c.DumpSave(""); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); cmd != "DUMP SAVE" {
		t.Errorf("command = %q, want %q", cmd, "DUMP SAVE")
	}
}

func TestDumpLoadWithFilepath(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK LOADED /tmp/dump.bin\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := c.DumpLoad("/tmp/dump.bin"); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); cmd != "DUMP LOAD /tmp/dump.bin" {
		t.Errorf("command = %q", cmd)
	}
}

func TestDumpLoadNoFilepath(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK LOADED default.dmp\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := c.DumpLoad(""); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); cmd != "DUMP LOAD" {
		t.Errorf("command = %q", cmd)
	}
}

func TestDumpStatus(t *testing.T) {
	response := "OK DUMP_STATUS\r\n" +
		"save_in_progress: true\r\n" +
		"load_in_progress: false\r\n" +
		"replication_paused_for_dump: true\r\n" +
		"status: SAVING\r\n" +
		"filepath: /backup/mygramdb.dmp\r\n" +
		"tables_processed: 2\r\n" +
		"tables_total: 5\r\n" +
		"current_table: users\r\n" +
		"elapsed_seconds: 3.45\r\n" +
		"END\r\n" +
		"\r\n"
	srv, addr, cleanup := newRecordingServer(t, response)
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	ds, err := c.DumpStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !ds.SaveInProgress {
		t.Error("SaveInProgress should be true")
	}
	if ds.LoadInProgress {
		t.Error("LoadInProgress should be false")
	}
	if ds.Status != "SAVING" {
		t.Errorf("Status = %q, want SAVING", ds.Status)
	}
	if ds.Filepath != "/backup/mygramdb.dmp" {
		t.Errorf("Filepath = %q", ds.Filepath)
	}
	if ds.TablesProcessed != 2 || ds.TablesTotal != 5 {
		t.Errorf("tables = %d/%d, want 2/5", ds.TablesProcessed, ds.TablesTotal)
	}
	if ds.CurrentTable != "users" {
		t.Errorf("CurrentTable = %q", ds.CurrentTable)
	}
	if ds.ElapsedSeconds != 3.45 {
		t.Errorf("ElapsedSeconds = %f", ds.ElapsedSeconds)
	}

	if cmd := srv.lastCommand(); cmd != "DUMP STATUS" {
		t.Errorf("command = %q", cmd)
	}
}

func TestDumpVerify(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK DUMP_VERIFIED /tmp/dump.bin\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	resp, err := c.DumpVerify("/tmp/dump.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp, "DUMP_VERIFIED") {
		t.Errorf("response = %q", resp)
	}
	if cmd := srv.lastCommand(); cmd != "DUMP VERIFY /tmp/dump.bin" {
		t.Errorf("command = %q", cmd)
	}
}

func TestDumpInfo(t *testing.T) {
	// DUMP INFO response is multi-line; the client returns the raw payload.
	response := "OK DUMP_INFO /tmp/dump.bin\r\n" +
		"version: 2\r\n" +
		"tables: 3\r\n" +
		"\r\n"
	srv, addr, cleanup := newRecordingServer(t, response)
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	resp, err := c.DumpInfo("/tmp/dump.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp, "version: 2") {
		t.Errorf("DumpInfo missing version line: %q", resp)
	}
	if cmd := srv.lastCommand(); cmd != "DUMP INFO /tmp/dump.bin" {
		t.Errorf("command = %q", cmd)
	}
}

// --- CACHE -------------------------------------------------------------------

func TestCacheStats(t *testing.T) {
	// Server emits `OK CACHE_STATS\r\n\r\n` followed by key: value lines.
	response := "OK CACHE_STATS\r\n\r\n" +
		"cache_enabled: 1\r\n" +
		"cache_hits: 120\r\n" +
		"cache_misses: 30\r\n" +
		"cache_hit_rate: 0.8000\r\n" +
		"cache_current_entries: 42\r\n" +
		"cache_memory_bytes: 1048576\r\n" +
		"cache_evictions: 5\r\n" +
		"\r\n"
	srv, addr, cleanup := newRecordingServer(t, response)
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	cs, err := c.CacheStats()
	if err != nil {
		t.Fatal(err)
	}
	if !cs.Enabled {
		t.Error("Enabled should be true")
	}
	if cs.Hits != 120 {
		t.Errorf("Hits = %d, want 120", cs.Hits)
	}
	if cs.Misses != 30 {
		t.Errorf("Misses = %d, want 30", cs.Misses)
	}
	if cs.HitRate != 0.8 {
		t.Errorf("HitRate = %f, want 0.8", cs.HitRate)
	}
	if cs.CurrentEntries != 42 {
		t.Errorf("CurrentEntries = %d, want 42", cs.CurrentEntries)
	}
	if cs.MemoryBytes != 1048576 {
		t.Errorf("MemoryBytes = %d", cs.MemoryBytes)
	}
	if cs.Evictions != 5 {
		t.Errorf("Evictions = %d, want 5", cs.Evictions)
	}

	if cmd := srv.lastCommand(); cmd != "CACHE STATS" {
		t.Errorf("command = %q", cmd)
	}
}

func TestCacheStatsDisabled(t *testing.T) {
	response := "OK CACHE_STATS\r\n\r\ncache_enabled: 0\r\n\r\n"
	_, addr, cleanup := newRecordingServer(t, response)
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	cs, err := c.CacheStats()
	if err != nil {
		t.Fatal(err)
	}
	if cs.Enabled {
		t.Error("Enabled should be false")
	}
}

func TestParseCacheStatsResponseInvalid(t *testing.T) {
	if _, err := parseCacheStatsResponse("NOPE"); err == nil {
		t.Error("expected error for bad prefix")
	}
}

func TestCacheClearAllTables(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK CACHE_CLEARED\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := c.CacheClear(""); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); cmd != "CACHE CLEAR" {
		t.Errorf("command = %q", cmd)
	}
}

func TestCacheClearSingleTable(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK CACHE_CLEARED table=articles\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := c.CacheClear("articles"); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); cmd != "CACHE CLEAR articles" {
		t.Errorf("command = %q", cmd)
	}
}

func TestCacheEnable(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK CACHE_ENABLED\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := c.CacheEnable(); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); cmd != "CACHE ENABLE" {
		t.Errorf("command = %q", cmd)
	}
}

func TestCacheDisable(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK CACHE_DISABLED\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := c.CacheDisable(); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); cmd != "CACHE DISABLE" {
		t.Errorf("command = %q", cmd)
	}
}

// --- OPTIMIZE ----------------------------------------------------------------

func TestOptimizeAllTables(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK OPTIMIZED terms=100 delta=10 roaring=90\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := c.Optimize(""); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); cmd != "OPTIMIZE" {
		t.Errorf("command = %q", cmd)
	}
}

func TestOptimizeSingleTable(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK OPTIMIZED terms=50 delta=5 roaring=45\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := c.Optimize("articles"); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); cmd != "OPTIMIZE articles" {
		t.Errorf("command = %q", cmd)
	}
}
