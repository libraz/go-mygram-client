package mygram

import (
	"testing"
)

func TestParseSearchResponse(t *testing.T) {
	t.Run("basic response", func(t *testing.T) {
		resp, err := parseSearchResponse("OK RESULTS 150 101 102 103")
		if err != nil {
			t.Fatal(err)
		}
		if resp.TotalCount != 150 {
			t.Errorf("TotalCount = %d, want 150", resp.TotalCount)
		}
		if len(resp.Results) != 3 {
			t.Fatalf("len(Results) = %d, want 3", len(resp.Results))
		}
		if resp.Results[0].PrimaryKey != "101" {
			t.Errorf("Results[0].PrimaryKey = %q, want %q", resp.Results[0].PrimaryKey, "101")
		}
		if resp.Results[2].PrimaryKey != "103" {
			t.Errorf("Results[2].PrimaryKey = %q, want %q", resp.Results[2].PrimaryKey, "103")
		}
		if resp.Debug != nil {
			t.Error("Debug should be nil")
		}
	})

	t.Run("zero results", func(t *testing.T) {
		resp, err := parseSearchResponse("OK RESULTS 0")
		if err != nil {
			t.Fatal(err)
		}
		if resp.TotalCount != 0 {
			t.Errorf("TotalCount = %d, want 0", resp.TotalCount)
		}
		if len(resp.Results) != 0 {
			t.Errorf("len(Results) = %d, want 0", len(resp.Results))
		}
	})

	t.Run("with debug info", func(t *testing.T) {
		response := "OK RESULTS 50 1 2 3\n# DEBUG\nquery_time: 1.5\nindex_time: 0.8\nterms: 3\ncache: hit\n"
		resp, err := parseSearchResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if resp.TotalCount != 50 {
			t.Errorf("TotalCount = %d, want 50", resp.TotalCount)
		}
		if resp.Debug == nil {
			t.Fatal("Debug should not be nil")
		}
		if resp.Debug.QueryTimeMs != 1.5 {
			t.Errorf("QueryTimeMs = %f, want 1.5", resp.Debug.QueryTimeMs)
		}
		if resp.Debug.Terms != 3 {
			t.Errorf("Terms = %d, want 3", resp.Debug.Terms)
		}
		if resp.Debug.Cache != "hit" {
			t.Errorf("Cache = %q, want %q", resp.Debug.Cache, "hit")
		}
	})

	t.Run("invalid response", func(t *testing.T) {
		_, err := parseSearchResponse("INVALID RESPONSE")
		if err == nil {
			t.Error("expected error")
		}
		if _, ok := err.(*ProtocolError); !ok {
			t.Errorf("expected ProtocolError, got %T", err)
		}
	})
}

func TestParseCountResponse(t *testing.T) {
	t.Run("basic response", func(t *testing.T) {
		resp, err := parseCountResponse("OK COUNT 1523")
		if err != nil {
			t.Fatal(err)
		}
		if resp.Count != 1523 {
			t.Errorf("Count = %d, want 1523", resp.Count)
		}
	})

	t.Run("with debug info", func(t *testing.T) {
		response := "OK COUNT 100\n# DEBUG\nquery_time: 0.5\nindex_time: 0.3\n"
		resp, err := parseCountResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Count != 100 {
			t.Errorf("Count = %d, want 100", resp.Count)
		}
		if resp.Debug == nil {
			t.Fatal("Debug should not be nil")
		}
		if resp.Debug.QueryTimeMs != 0.5 {
			t.Errorf("QueryTimeMs = %f, want 0.5", resp.Debug.QueryTimeMs)
		}
	})
}

func TestParseGetResponse(t *testing.T) {
	t.Run("document with fields", func(t *testing.T) {
		resp, err := parseGetResponse("OK DOC 12345 status=1 category=tech created_at=1705312200")
		if err != nil {
			t.Fatal(err)
		}
		if resp.PrimaryKey != "12345" {
			t.Errorf("PrimaryKey = %q, want %q", resp.PrimaryKey, "12345")
		}
		if resp.Fields["status"] != "1" {
			t.Errorf("Fields[status] = %q, want %q", resp.Fields["status"], "1")
		}
		if resp.Fields["category"] != "tech" {
			t.Errorf("Fields[category] = %q, want %q", resp.Fields["category"], "tech")
		}
	})

	t.Run("document without fields", func(t *testing.T) {
		resp, err := parseGetResponse("OK DOC 99999")
		if err != nil {
			t.Fatal(err)
		}
		if resp.PrimaryKey != "99999" {
			t.Errorf("PrimaryKey = %q, want %q", resp.PrimaryKey, "99999")
		}
		if len(resp.Fields) != 0 {
			t.Errorf("len(Fields) = %d, want 0", len(resp.Fields))
		}
	})
}

func TestParseInfoResponse(t *testing.T) {
	response := `OK INFO

# Server
version: 2.1.0
uptime_seconds: 3600

# Stats
total_requests: 50000

# Clients
connected_clients: 5

# Memory
used_memory_bytes: 1048576

# Index
total_documents: 100000

# Tables
tables: threads,images,keywords
`

	info, err := parseInfoResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "2.1.0" {
		t.Errorf("Version = %q, want %q", info.Version, "2.1.0")
	}
	if info.UptimeSeconds != 3600 {
		t.Errorf("UptimeSeconds = %d, want 3600", info.UptimeSeconds)
	}
	if info.TotalRequests != 50000 {
		t.Errorf("TotalRequests = %d, want 50000", info.TotalRequests)
	}
	if info.ActiveConnections != 5 {
		t.Errorf("ActiveConnections = %d, want 5", info.ActiveConnections)
	}
	if info.DocCount != 100000 {
		t.Errorf("DocCount = %d, want 100000", info.DocCount)
	}
	if len(info.Tables) != 3 {
		t.Fatalf("len(Tables) = %d, want 3", len(info.Tables))
	}
	if info.Tables[0] != "threads" {
		t.Errorf("Tables[0] = %q, want %q", info.Tables[0], "threads")
	}
}

func TestParseReplicationStatusResponse(t *testing.T) {
	t.Run("multi-line format", func(t *testing.T) {
		response := "OK REPLICATION\nstatus: running\ncurrent_gtid: abc-123\nprocessed_events: 100\nEND"
		status, err := parseReplicationStatusResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if !status.Running {
			t.Error("Running should be true")
		}
		if status.GTID != "abc-123" {
			t.Errorf("GTID = %q, want %q", status.GTID, "abc-123")
		}
	})

	t.Run("single-line format", func(t *testing.T) {
		response := "OK REPLICATION status=stopped gtid=xyz-789"
		status, err := parseReplicationStatusResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if status.Running {
			t.Error("Running should be false")
		}
		if status.GTID != "xyz-789" {
			t.Errorf("GTID = %q, want %q", status.GTID, "xyz-789")
		}
	})

	t.Run("multi-line with processed_events and queue_size", func(t *testing.T) {
		response := "OK REPLICATION\nstatus: running\ncurrent_gtid: abc-123\nprocessed_events: 1234\nqueue_size: 5\nEND"
		status, err := parseReplicationStatusResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if status.ProcessedEvents != 1234 {
			t.Errorf("ProcessedEvents = %d, want 1234", status.ProcessedEvents)
		}
		if status.QueueSize != 5 {
			t.Errorf("QueueSize = %d, want 5", status.QueueSize)
		}
	})

	t.Run("processed_events defaults to zero", func(t *testing.T) {
		response := "OK REPLICATION\nstatus: running\ncurrent_gtid: abc\nEND"
		status, err := parseReplicationStatusResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if status.ProcessedEvents != 0 {
			t.Errorf("ProcessedEvents = %d, want 0", status.ProcessedEvents)
		}
		if status.QueueSize != 0 {
			t.Errorf("QueueSize = %d, want 0", status.QueueSize)
		}
	})

	t.Run("gtid key alias accepted", func(t *testing.T) {
		// Some server builds emit `gtid:` instead of `current_gtid:`.
		response := "OK REPLICATION\nstatus: stopped\ngtid: aaa-bbb\nEND"
		status, err := parseReplicationStatusResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if status.GTID != "aaa-bbb" {
			t.Errorf("GTID = %q, want %q", status.GTID, "aaa-bbb")
		}
		if status.Running {
			t.Error("Running should be false")
		}
	})
}

func TestParseDumpStatusResponse(t *testing.T) {
	response := `OK DUMP_STATUS
save_in_progress: false
load_in_progress: false
status: IDLE
filepath: /tmp/dump.bin
tables_processed: 3
tables_total: 5
elapsed_seconds: 1.5
END
`
	ds, err := parseDumpStatusResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if ds.SaveInProgress {
		t.Error("SaveInProgress should be false")
	}
	if ds.Status != "IDLE" {
		t.Errorf("Status = %q, want %q", ds.Status, "IDLE")
	}
	if ds.TablesProcessed != 3 {
		t.Errorf("TablesProcessed = %d, want 3", ds.TablesProcessed)
	}
	if ds.ElapsedSeconds != 1.5 {
		t.Errorf("ElapsedSeconds = %f, want 1.5", ds.ElapsedSeconds)
	}
}

func TestParseDebugInfo(t *testing.T) {
	lines := []string{
		"query_time: 45.23",
		"index_time: 12.34",
		"filter_time: 2.10",
		"terms: 3",
		"ngrams: 10",
		"candidates: 1000",
		"after_intersection: 500",
		"after_not: 450",
		"after_filters: 100",
		"final: 50",
		"optimization: fast_path",
		"cache: miss",
		"limit: 100 (default)",
		"offset: 0 (default)",
	}

	debug := parseDebugInfo(lines)
	if debug.QueryTimeMs != 45.23 {
		t.Errorf("QueryTimeMs = %f, want 45.23", debug.QueryTimeMs)
	}
	if debug.IndexTimeMs != 12.34 {
		t.Errorf("IndexTimeMs = %f, want 12.34", debug.IndexTimeMs)
	}
	if debug.Terms != 3 {
		t.Errorf("Terms = %d, want 3", debug.Terms)
	}
	if debug.Candidates != 1000 {
		t.Errorf("Candidates = %d, want 1000", debug.Candidates)
	}
	if debug.Final != 50 {
		t.Errorf("Final = %d, want 50", debug.Final)
	}
	if debug.Optimization != "fast_path" {
		t.Errorf("Optimization = %q, want %q", debug.Optimization, "fast_path")
	}
	if debug.Cache != "miss" {
		t.Errorf("Cache = %q, want %q", debug.Cache, "miss")
	}
}
