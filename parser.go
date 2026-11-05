package mygram

import (
	"fmt"
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
//
// Primary keys are reversibly escaped per spec/tcp-commands.md §11.1 when
// they are empty or contain whitespace, a quote, a backslash or a control
// character, so both formats decode them with parseWireToken (wire_quoting.go)
// rather than splitting on raw whitespace or a raw tab.
func parseSearchResponse(response string) (*SearchResponse, error) {
	// Normalize on \n for easier line handling; callers have already
	// collapsed \r\n -> \n, but we guard here for completeness.
	normalized := strings.ReplaceAll(response, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	firstLine := lines[0]

	if !strings.HasPrefix(firstLine, "OK RESULTS ") {
		return nil, &ProtocolError{Message: "invalid SEARCH response: " + firstLine}
	}

	pos := 0
	var ok bool
	if _, pos, ok = parseWireToken(firstLine, pos); !ok { // "OK"
		return nil, &ProtocolError{Message: "invalid SEARCH response: missing status"}
	}
	if _, pos, ok = parseWireToken(firstLine, pos); !ok { // "RESULTS"
		return nil, &ProtocolError{Message: "invalid SEARCH response: missing RESULTS"}
	}
	totalCountStr, pos, ok := parseWireToken(firstLine, pos)
	if !ok {
		return nil, &ProtocolError{Message: "invalid SEARCH response: missing total count"}
	}
	totalCount, err := strconv.Atoi(totalCountStr)
	if err != nil {
		return nil, &ProtocolError{Message: "invalid total count: " + totalCountStr}
	}

	// Remaining tokens on the header line are reversibly escaped primary
	// keys (classic mode); HIGHLIGHT mode leaves nothing here.
	var classicPKs []string
	for {
		for pos < len(firstLine) && isASCIISpaceByte(firstLine[pos]) {
			pos++
		}
		if pos >= len(firstLine) {
			break
		}
		tok, next, tokOK := parseWireToken(firstLine, pos)
		if !tokOK {
			return nil, &ProtocolError{Message: "invalid SEARCH response: malformed primary key"}
		}
		pos = next
		classicPKs = append(classicPKs, tok)
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
			pkWire, snippet := line, ""
			if tab := strings.IndexByte(line, '\t'); tab >= 0 {
				pkWire, snippet = line[:tab], line[tab+1:]
			}
			pk, endPos, pkOK := parseWireToken(pkWire, 0)
			if !pkOK || endPos != len(pkWire) {
				return nil, &ProtocolError{Message: "invalid SEARCH response: malformed primary key"}
			}
			results = append(results, SearchResult{PrimaryKey: pk, Snippet: snippet})
		}
	} else {
		// Classic mode: PKs follow the count on the first line.
		results = make([]SearchResult, 0, len(classicPKs))
		for _, pk := range classicPKs {
			results = append(results, SearchResult{PrimaryKey: pk})
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
// Format (MygramDB >= v1.9):
//
//	OK FACET <num_values> <total_values>
//	<value1>\t<count1>
//	<value2>\t<count2>
//	...
//	[# query_time_ms: <n>]
//	[# total_docs_searched: <n>]
//
// <num_values> is the size of the returned page and <total_values> the number
// of distinct values before OFFSET and LIMIT. A server that predates paginated
// facets omits the second number, in which case the page size is reported as
// the total.
func parseFacetResponse(response string) (*FacetResponse, error) {
	normalized := strings.ReplaceAll(response, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	firstLine := lines[0]

	if !strings.HasPrefix(firstLine, "OK FACET") {
		return nil, &ProtocolError{Message: "invalid FACET response: " + firstLine}
	}

	parts := strings.Fields(firstLine)
	if len(parts) < 3 {
		return nil, &ProtocolError{Message: "invalid FACET response: missing count"}
	}
	if len(parts) > 4 {
		return nil, &ProtocolError{Message: "invalid FACET response header: " + firstLine}
	}
	expected, err := strconv.Atoi(parts[2])
	if err != nil || expected < 0 {
		return nil, &ProtocolError{Message: "invalid FACET count: " + parts[2]}
	}
	total := int64(expected)
	if len(parts) == 4 {
		total, err = strconv.ParseInt(parts[3], 10, 64)
		if err != nil || total < int64(expected) {
			return nil, &ProtocolError{Message: "invalid FACET total count: " + parts[3]}
		}
	}

	results := make([]FacetValue, 0, expected)
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		// A line starting with '#' is a comment (e.g. "# DEBUG") only when it
		// has no tab. A facet value may legitimately start with '#', in which
		// case the line still has the "<value>\t<count>" shape and is data.
		if strings.HasPrefix(line, "#") && tab < 0 {
			continue
		}
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

	// The header's page size is authoritative: a mismatch means rows were lost
	// or a value carried an embedded newline, either of which would silently
	// truncate the caller's page.
	if len(results) != expected {
		return nil, &ProtocolError{
			Message: fmt.Sprintf("FACET row count mismatch: header says %d, got %d", expected, len(results)),
		}
	}

	return &FacetResponse{Results: results, TotalCount: total}, nil
}

// parseCountResponse parses a COUNT response.
// Format: OK COUNT <number>
//
// The header is parsed strictly: the count must be a non-negative decimal and
// the only payload on the line, so a malformed or truncated frame is reported
// instead of silently yielding a plausible number.
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
	if len(parts) > 3 {
		return nil, &ProtocolError{Message: "invalid COUNT response header: " + firstLine}
	}

	count, err := strconv.Atoi(parts[2])
	if err != nil || count < 0 {
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
// Format: OK DOC <primary_key>[ <field>=<value>]...
//
// The primary key and every string-typed field value are reversibly escaped
// per spec/tcp-commands.md §11.1 when empty or containing whitespace, a
// quote, a backslash or a control character, so both are decoded with
// parseWireToken (wire_quoting.go) rather than split on raw whitespace or
// '=', which would otherwise fragment a quoted key or value containing a
// space. A field's name is never quoted (it is an identifier), so the '='
// immediately following it is a reliable boundary between name and value.
func parseGetResponse(response string) (*Document, error) {
	firstLine := strings.Split(response, "\n")[0]

	if !strings.HasPrefix(firstLine, "OK DOC ") {
		return nil, &ProtocolError{Message: "invalid GET response: " + firstLine}
	}

	pos := 0
	var ok bool
	if _, pos, ok = parseWireToken(firstLine, pos); !ok { // "OK"
		return nil, &ProtocolError{Message: "invalid GET response: missing status"}
	}
	if _, pos, ok = parseWireToken(firstLine, pos); !ok { // "DOC"
		return nil, &ProtocolError{Message: "invalid GET response: missing DOC"}
	}
	pk, pos, ok := parseWireToken(firstLine, pos)
	if !ok {
		return nil, &ProtocolError{Message: "invalid GET response: missing primary key"}
	}

	fields := make(map[string]string)
	for pos < len(firstLine) {
		for pos < len(firstLine) && isASCIISpaceByte(firstLine[pos]) {
			pos++
		}
		keyStart := pos
		for pos < len(firstLine) && firstLine[pos] != '=' && !isASCIISpaceByte(firstLine[pos]) {
			pos++
		}
		if pos >= len(firstLine) || firstLine[pos] != '=' {
			continue // a token without '=' carries no field
		}
		name := firstLine[keyStart:pos]
		pos++ // skip '='

		// An empty value is always quoted on the wire, so a '=' directly
		// before whitespace or the end of the line is an empty bare token.
		value := ""
		if pos < len(firstLine) && !isASCIISpaceByte(firstLine[pos]) {
			var valueOK bool
			if value, pos, valueOK = parseWireToken(firstLine, pos); !valueOK {
				return nil, &ProtocolError{Message: "invalid GET response: malformed field value"}
			}
		}
		if name != "" {
			fields[name] = value
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
		case "data_initialized":
			info.DataInitialized = value == "true"
		case "readiness":
			info.Readiness = value
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
				status.State = value
			case "current_gtid", "gtid":
				status.GTID = value
			case "processed_events":
				status.ProcessedEvents, _ = strconv.Atoi(value)
			case "queue_size":
				status.QueueSize, _ = strconv.Atoi(value)
			case "crc_errors":
				status.CRCErrors, _ = strconv.Atoi(value)
			case "schema_incompatible":
				status.SchemaIncompatible = value == "true"
			case "last_error_code":
				// The server reports 0 for "no failure recorded", which is
				// what ErrCodeNone already means, so it needs no special case.
				if code, err := strconv.ParseUint(value, 10, 16); err == nil {
					status.LastErrorCode = ErrorCode(code)
				}
			case "last_error":
				status.LastError = value
			case "last_applied_unixtime":
				status.LastAppliedUnixtime, _ = strconv.ParseInt(value, 10, 64)
			case "seconds_since_last_applied":
				status.SecondsSinceLastApplied, _ = strconv.ParseInt(value, 10, 64)
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
		case "enabled":
			cs.Enabled = value == "true"
		case "total_queries":
			cs.TotalQueries, _ = strconv.Atoi(value)
		case "cache_hits":
			cs.Hits, _ = strconv.Atoi(value)
		case "cache_misses":
			cs.Misses, _ = strconv.Atoi(value)
		case "hit_rate":
			cs.HitRate, _ = strconv.ParseFloat(value, 64)
		case "current_entries":
			cs.CurrentEntries, _ = strconv.Atoi(value)
		case "current_memory_bytes":
			cs.MemoryBytes, _ = strconv.Atoi(value)
		case "invalidation_index_memory_bytes":
			cs.InvalidationIndexMemoryBytes, _ = strconv.Atoi(value)
		case "invalidation_queue_memory_bytes":
			cs.InvalidationQueueMemoryBytes, _ = strconv.Atoi(value)
		case "accounted_memory_bytes":
			cs.AccountedMemoryBytes, _ = strconv.Atoi(value)
		case "evictions":
			cs.Evictions, _ = strconv.Atoi(value)
		case "ttl_expirations":
			cs.TTLExpirations, _ = strconv.Atoi(value)
		case "rejection_count":
			cs.RejectionCount, _ = strconv.Atoi(value)
		case "rejection_oversize":
			cs.RejectionOversize, _ = strconv.Atoi(value)
		case "rejection_memory_budget":
			cs.RejectionMemoryBudget, _ = strconv.Atoi(value)
		case "rejection_duplicate":
			cs.RejectionDuplicate, _ = strconv.Atoi(value)
		case "stale_entry_removals":
			cs.StaleEntryRemovals, _ = strconv.Atoi(value)
		case "decompression_failures":
			cs.DecompressionFailures, _ = strconv.Atoi(value)
		case "stale_lru_entries":
			cs.StaleLRUEntries, _ = strconv.Atoi(value)
		case "invalidations_immediate":
			cs.InvalidationsImmediate, _ = strconv.Atoi(value)
		case "invalidations_deferred":
			cs.InvalidationsDeferred, _ = strconv.Atoi(value)
		case "invalidations_batches":
			cs.InvalidationsBatches, _ = strconv.Atoi(value)
		case "avg_cache_hit_time_ms":
			if f, err := strconv.ParseFloat(value, 64); err == nil {
				cs.AvgHitTimeMs = &f
			}
		case "avg_cache_miss_time_ms":
			if f, err := strconv.ParseFloat(value, 64); err == nil {
				cs.AvgMissTimeMs = &f
			}
		case "total_time_saved_ms":
			cs.TotalTimeSavedMs, _ = strconv.ParseFloat(value, 64)
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
