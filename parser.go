package mygram

import (
	"strconv"
	"strings"
)

// parseSearchResponse parses a SEARCH response.
//
// Two formats are supported:
//
//  1. Standard (single-line results):
//     OK RESULTS <total_count> <id1> <id2> ...
//
//  2. HIGHLIGHT (multi-line results, introduced in MygramDB v1.6):
//     OK RESULTS <total_count>
//     <id1>\t<snippet1>
//     <id2>\t<snippet2>
//     ...
//
// Both formats optionally append a `# DEBUG` block terminated by \r\n\r\n.
func parseSearchResponse(response string) (*SearchResponse, error) {
	// Normalize on \n for easier line handling; callers have already
	// collapsed \r\n -> \n, but we guard here for completeness.
	normalized := strings.ReplaceAll(response, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	firstLine := lines[0]

	if !strings.HasPrefix(firstLine, "OK RESULTS ") {
		return nil, &ProtocolError{Message: "invalid SEARCH response: " + firstLine}
	}

	parts := strings.Fields(firstLine)
	if len(parts) < 3 {
		return nil, &ProtocolError{Message: "invalid SEARCH response: missing total count"}
	}

	totalCount, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, &ProtocolError{Message: "invalid total count: " + parts[2]}
	}

	// Collect any subsequent lines that precede the debug block. If any
	// such line contains a tab, it is a HIGHLIGHT result row. Otherwise
	// the response is the classic single-line format.
	var highlightLines []string
	debugStart := -1
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if line == "# DEBUG" {
			debugStart = i + 1
			break
		}
		if line == "" {
			continue
		}
		highlightLines = append(highlightLines, line)
	}

	var results []SearchResult
	if len(highlightLines) > 0 {
		// HIGHLIGHT mode: first line contains only the count.
		results = make([]SearchResult, 0, len(highlightLines))
		for _, line := range highlightLines {
			pk := line
			snippet := ""
			if tab := strings.IndexByte(line, '\t'); tab >= 0 {
				pk = line[:tab]
				snippet = line[tab+1:]
			}
			results = append(results, SearchResult{PrimaryKey: pk, Snippet: snippet})
		}
	} else {
		// Classic mode: PKs follow the count on the first line.
		results = make([]SearchResult, 0, len(parts)-3)
		for _, id := range parts[3:] {
			results = append(results, SearchResult{PrimaryKey: id})
		}
	}

	var debug *DebugInfo
	if debugStart >= 0 && debugStart < len(lines) {
		debug = parseDebugInfo(lines[debugStart:])
	}

	return &SearchResponse{
		Results:    results,
		TotalCount: totalCount,
		Debug:      debug,
	}, nil
}

// parseFacetResponse parses a FACET response.
//
// Format (MygramDB >= v1.6):
//
//	OK FACET <num_values>
//	<value1>\t<count1>
//	<value2>\t<count2>
//	...
//	[# query_time_ms: <n>]
//	[# total_docs_searched: <n>]
func parseFacetResponse(response string) (*FacetResponse, error) {
	normalized := strings.ReplaceAll(response, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	firstLine := lines[0]

	if !strings.HasPrefix(firstLine, "OK FACET") {
		return nil, &ProtocolError{Message: "invalid FACET response: " + firstLine}
	}

	// First line is "OK FACET <num_values>"; count is the third token.
	parts := strings.Fields(firstLine)
	if len(parts) < 3 {
		return nil, &ProtocolError{Message: "invalid FACET response: missing count"}
	}
	expected, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, &ProtocolError{Message: "invalid FACET count: " + parts[2]}
	}

	results := make([]FacetValue, 0, expected)
	for _, line := range lines[1:] {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			return nil, &ProtocolError{Message: "invalid FACET row: " + line}
		}
		value := line[:tab]
		countStr := strings.TrimSpace(line[tab+1:])
		count, err := strconv.ParseInt(countStr, 10, 64)
		if err != nil {
			return nil, &ProtocolError{Message: "invalid FACET count for " + value + ": " + countStr}
		}
		results = append(results, FacetValue{Value: value, Count: count})
	}

	return &FacetResponse{Results: results}, nil
}

// parseCountResponse parses a COUNT response.
// Format: OK COUNT <number>
func parseCountResponse(response string) (*CountResponse, error) {
	lines := strings.Split(response, "\n")
	firstLine := lines[0]

	if !strings.HasPrefix(firstLine, "OK COUNT ") {
		return nil, &ProtocolError{Message: "invalid COUNT response: " + firstLine}
	}

	parts := strings.Fields(firstLine)
	if len(parts) < 3 {
		return nil, &ProtocolError{Message: "invalid COUNT response: missing count"}
	}

	count, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, &ProtocolError{Message: "invalid count: " + parts[2]}
	}

	var debug *DebugInfo
	for i, line := range lines {
		if line == "# DEBUG" && i+1 < len(lines) {
			debug = parseDebugInfo(lines[i+1:])
			break
		}
	}

	return &CountResponse{
		Count: count,
		Debug: debug,
	}, nil
}

// parseGetResponse parses a GET response.
// Format: OK DOC <primary_key> <field1=value1> <field2=value2> ...
func parseGetResponse(response string) (*Document, error) {
	firstLine := strings.Split(response, "\n")[0]

	if !strings.HasPrefix(firstLine, "OK DOC ") {
		return nil, &ProtocolError{Message: "invalid GET response: " + firstLine}
	}

	parts := strings.Fields(firstLine)
	if len(parts) < 3 {
		return nil, &ProtocolError{Message: "invalid GET response: missing primary key"}
	}

	pk := parts[2]
	fields := make(map[string]string)
	for _, kv := range parts[3:] {
		idx := strings.Index(kv, "=")
		if idx > 0 {
			fields[kv[:idx]] = kv[idx+1:]
		}
	}

	return &Document{
		PrimaryKey: pk,
		Fields:     fields,
	}, nil
}

// parseInfoResponse parses an INFO response.
func parseInfoResponse(response string) (*ServerInfo, error) {
	if !strings.HasPrefix(response, "OK INFO") {
		return nil, &ProtocolError{Message: "invalid INFO response: " + response[:min(len(response), 50)]}
	}

	info := &ServerInfo{}
	lines := strings.Split(response, "\n")
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		idx := strings.Index(trimmed, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		value := strings.TrimSpace(trimmed[idx+1:])

		switch key {
		case "version":
			info.Version = value
		case "uptime_seconds":
			info.UptimeSeconds, _ = strconv.Atoi(value)
		case "total_requests":
			info.TotalRequests, _ = strconv.Atoi(value)
		case "connected_clients":
			info.ActiveConnections, _ = strconv.Atoi(value)
		case "used_memory_bytes":
			info.IndexSizeBytes, _ = strconv.Atoi(value)
		case "total_documents":
			info.DocCount, _ = strconv.Atoi(value)
		case "tables":
			if value != "" {
				tables := strings.Split(value, ",")
				info.Tables = make([]string, len(tables))
				for i, t := range tables {
					info.Tables[i] = strings.TrimSpace(t)
				}
			}
		}
	}

	return info, nil
}

// parseReplicationStatusResponse parses a REPLICATION STATUS response.
func parseReplicationStatusResponse(response string) (*ReplicationStatus, error) {
	if !strings.HasPrefix(response, "OK REPLICATION") {
		return nil, &ProtocolError{Message: "invalid REPLICATION STATUS response: " + response[:min(len(response), 50)]}
	}

	lines := strings.Split(response, "\n")

	// Multi-line format
	if strings.TrimSpace(lines[0]) == "OK REPLICATION" {
		status := &ReplicationStatus{StatusStr: response}
		for _, line := range lines[1:] {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || trimmed == "END" {
				continue
			}
			idx := strings.Index(trimmed, ":")
			if idx < 0 {
				continue
			}
			key := strings.TrimSpace(trimmed[:idx])
			value := strings.TrimSpace(trimmed[idx+1:])

			switch key {
			case "status":
				status.Running = value == "running"
			case "current_gtid", "gtid":
				status.GTID = value
			case "processed_events":
				status.ProcessedEvents, _ = strconv.Atoi(value)
			case "queue_size":
				status.QueueSize, _ = strconv.Atoi(value)
			}
		}
		return status, nil
	}

	// Single-line format: OK REPLICATION status=running gtid=xxx
	status := &ReplicationStatus{StatusStr: response}
	parts := strings.Fields(response)
	for _, p := range parts[2:] {
		if strings.HasPrefix(p, "status=") {
			status.Running = p[7:] == "running"
		} else if strings.HasPrefix(p, "gtid=") {
			status.GTID = p[5:]
		}
	}
	return status, nil
}

// parseDumpStatusResponse parses a DUMP STATUS response.
func parseDumpStatusResponse(response string) (*DumpStatus, error) {
	if !strings.HasPrefix(response, "OK DUMP_STATUS") {
		return nil, &ProtocolError{Message: "invalid DUMP STATUS response"}
	}

	ds := &DumpStatus{}
	lines := strings.Split(response, "\n")
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "END" {
			continue
		}
		idx := strings.Index(trimmed, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		value := strings.TrimSpace(trimmed[idx+1:])

		switch key {
		case "save_in_progress":
			ds.SaveInProgress = value == "true"
		case "load_in_progress":
			ds.LoadInProgress = value == "true"
		case "status":
			ds.Status = value
		case "filepath":
			ds.Filepath = value
		case "tables_processed":
			ds.TablesProcessed, _ = strconv.Atoi(value)
		case "tables_total":
			ds.TablesTotal, _ = strconv.Atoi(value)
		case "current_table":
			ds.CurrentTable = value
		case "elapsed_seconds":
			ds.ElapsedSeconds, _ = strconv.ParseFloat(value, 64)
		case "result_filepath":
			ds.ResultFilepath = value
		case "error":
			ds.Error = value
		}
	}
	return ds, nil
}

// parseCacheStatsResponse parses a CACHE STATS response.
func parseCacheStatsResponse(response string) (*CacheStats, error) {
	if !strings.HasPrefix(response, "OK CACHE_STATS") {
		return nil, &ProtocolError{Message: "invalid CACHE STATS response"}
	}

	cs := &CacheStats{}
	lines := strings.Split(response, "\n")
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		idx := strings.Index(trimmed, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		value := strings.TrimSpace(trimmed[idx+1:])

		switch key {
		case "cache_enabled":
			cs.Enabled = value == "1"
		case "cache_hits":
			cs.Hits, _ = strconv.Atoi(value)
		case "cache_misses":
			cs.Misses, _ = strconv.Atoi(value)
		case "cache_hit_rate":
			cs.HitRate, _ = strconv.ParseFloat(value, 64)
		case "cache_current_entries":
			cs.CurrentEntries, _ = strconv.Atoi(value)
		case "cache_memory_bytes":
			cs.MemoryBytes, _ = strconv.Atoi(value)
		case "cache_evictions":
			cs.Evictions, _ = strconv.Atoi(value)
		}
	}
	return cs, nil
}

// parseDebugInfo parses a debug information section.
func parseDebugInfo(lines []string) *DebugInfo {
	debug := &DebugInfo{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		idx := strings.Index(trimmed, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		value := strings.TrimSpace(trimmed[idx+1:])

		switch key {
		case "query_time":
			debug.QueryTimeMs, _ = strconv.ParseFloat(value, 64)
		case "index_time":
			debug.IndexTimeMs, _ = strconv.ParseFloat(value, 64)
		case "filter_time":
			debug.FilterTimeMs, _ = strconv.ParseFloat(value, 64)
		case "terms":
			debug.Terms, _ = strconv.Atoi(value)
		case "ngrams":
			debug.Ngrams, _ = strconv.Atoi(value)
		case "candidates":
			debug.Candidates, _ = strconv.Atoi(value)
		case "after_intersection":
			debug.AfterIntersection, _ = strconv.Atoi(value)
		case "after_not":
			debug.AfterNot, _ = strconv.Atoi(value)
		case "after_filters":
			debug.AfterFilters, _ = strconv.Atoi(value)
		case "final":
			debug.Final, _ = strconv.Atoi(value)
		case "optimization":
			debug.Optimization = value
		case "sort":
			debug.Sort = value
		case "cache":
			debug.Cache = value
		case "cache_age_ms":
			debug.CacheAgeMs, _ = strconv.ParseFloat(value, 64)
		case "cache_saved_ms":
			debug.CacheSavedMs, _ = strconv.ParseFloat(value, 64)
		case "limit":
			debug.Limit, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(value), "(default)"))
		case "offset":
			debug.Offset, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(value), "(default)"))
		}
	}
	return debug
}
