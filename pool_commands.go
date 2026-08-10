package mygram

import "context"

// This file mirrors Client's command surface on Pool. Each method borrows a
// connection via run (which applies reconnection and, for safe failures, the
// retry policy) and delegates to the matching Client method. The idempotent
// flag passed to run classifies the command: read-only commands may be retried
// after a read-phase failure, state-changing commands may not.
//
// TestPoolMatchesClientContextMethods verifies that this surface stays in sync
// with Client's *Context methods.

// SendCommand sends a raw command to the server and returns the response.
func (p *Pool) SendCommand(command string) (string, error) {
	return p.SendCommandContext(context.Background(), command)
}

// SendCommandContext sends a raw command with the given context. Raw commands
// are treated as state-changing and are never retried after they reach the
// server.
func (p *Pool) SendCommandContext(ctx context.Context, command string) (string, error) {
	var out string
	err := p.run(ctx, false, func(c *Client) error {
		var e error
		out, e = c.SendCommandContext(ctx, command)
		return e
	})
	return out, err
}

// Search performs a full-text search.
func (p *Pool) Search(table, query string, opts SearchOptions) (*SearchResponse, error) {
	return p.SearchContext(context.Background(), table, query, opts)
}

// SearchContext performs a full-text search with the given context.
func (p *Pool) SearchContext(ctx context.Context, table, query string, opts SearchOptions) (*SearchResponse, error) {
	var out *SearchResponse
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.SearchContext(ctx, table, query, opts)
		return e
	})
	return out, err
}

// SearchWithHighlights is a Search variant that requests highlighted snippets.
func (p *Pool) SearchWithHighlights(table, query string, opts SearchOptions) (*SearchResponse, error) {
	return p.SearchWithHighlightsContext(context.Background(), table, query, opts)
}

// SearchWithHighlightsContext is the context-aware variant of SearchWithHighlights.
func (p *Pool) SearchWithHighlightsContext(ctx context.Context, table, query string, opts SearchOptions) (*SearchResponse, error) {
	var out *SearchResponse
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.SearchWithHighlightsContext(ctx, table, query, opts)
		return e
	})
	return out, err
}

// SearchRaw searches using a pre-built boolean expression.
func (p *Pool) SearchRaw(table, rawQuery string, opts SearchRawOptions) (*SearchResponse, error) {
	return p.SearchRawContext(context.Background(), table, rawQuery, opts)
}

// SearchRawContext is the context-aware variant of SearchRaw.
func (p *Pool) SearchRawContext(ctx context.Context, table, rawQuery string, opts SearchRawOptions) (*SearchResponse, error) {
	var out *SearchResponse
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.SearchRawContext(ctx, table, rawQuery, opts)
		return e
	})
	return out, err
}

// SearchRawWithHighlights is a SearchRaw variant that requests highlighted snippets.
func (p *Pool) SearchRawWithHighlights(table, rawQuery string, opts SearchRawOptions) (*SearchResponse, error) {
	return p.SearchRawWithHighlightsContext(context.Background(), table, rawQuery, opts)
}

// SearchRawWithHighlightsContext is the context-aware variant of SearchRawWithHighlights.
func (p *Pool) SearchRawWithHighlightsContext(ctx context.Context, table, rawQuery string, opts SearchRawOptions) (*SearchResponse, error) {
	var out *SearchResponse
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.SearchRawWithHighlightsContext(ctx, table, rawQuery, opts)
		return e
	})
	return out, err
}

// Facet aggregates distinct values of a filter column with their document counts.
func (p *Pool) Facet(table, column string, opts FacetOptions) (*FacetResponse, error) {
	return p.FacetContext(context.Background(), table, column, opts)
}

// FacetContext is the context-aware variant of Facet.
func (p *Pool) FacetContext(ctx context.Context, table, column string, opts FacetOptions) (*FacetResponse, error) {
	var out *FacetResponse
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.FacetContext(ctx, table, column, opts)
		return e
	})
	return out, err
}

// Count returns the number of matching documents.
func (p *Pool) Count(table, query string, opts CountOptions) (*CountResponse, error) {
	return p.CountContext(context.Background(), table, query, opts)
}

// CountContext performs a count query with the given context.
func (p *Pool) CountContext(ctx context.Context, table, query string, opts CountOptions) (*CountResponse, error) {
	var out *CountResponse
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.CountContext(ctx, table, query, opts)
		return e
	})
	return out, err
}

// Get retrieves a document by its primary key.
func (p *Pool) Get(table, primaryKey string) (*Document, error) {
	return p.GetContext(context.Background(), table, primaryKey)
}

// GetContext retrieves a document by its primary key with the given context.
func (p *Pool) GetContext(ctx context.Context, table, primaryKey string) (*Document, error) {
	var out *Document
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.GetContext(ctx, table, primaryKey)
		return e
	})
	return out, err
}

// Info retrieves server information.
func (p *Pool) Info() (*ServerInfo, error) {
	return p.InfoContext(context.Background())
}

// InfoContext retrieves server information with the given context.
func (p *Pool) InfoContext(ctx context.Context) (*ServerInfo, error) {
	var out *ServerInfo
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.InfoContext(ctx)
		return e
	})
	return out, err
}

// GetConfig retrieves server configuration in YAML format.
func (p *Pool) GetConfig() (string, error) {
	return p.GetConfigContext(context.Background())
}

// GetConfigContext retrieves server configuration with the given context.
func (p *Pool) GetConfigContext(ctx context.Context) (string, error) {
	var out string
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.GetConfigContext(ctx)
		return e
	})
	return out, err
}

// GetReplicationStatus retrieves the replication status.
func (p *Pool) GetReplicationStatus() (*ReplicationStatus, error) {
	return p.GetReplicationStatusContext(context.Background())
}

// GetReplicationStatusContext retrieves the replication status with the given context.
func (p *Pool) GetReplicationStatusContext(ctx context.Context) (*ReplicationStatus, error) {
	var out *ReplicationStatus
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.GetReplicationStatusContext(ctx)
		return e
	})
	return out, err
}

// DumpStatus retrieves the status of a dump operation.
func (p *Pool) DumpStatus() (*DumpStatus, error) {
	return p.DumpStatusContext(context.Background())
}

// DumpStatusContext retrieves the status of a dump operation with the given context.
func (p *Pool) DumpStatusContext(ctx context.Context) (*DumpStatus, error) {
	var out *DumpStatus
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.DumpStatusContext(ctx)
		return e
	})
	return out, err
}

// DumpVerify verifies the integrity of a dump file.
func (p *Pool) DumpVerify(filepath string) (string, error) {
	return p.DumpVerifyContext(context.Background(), filepath)
}

// DumpVerifyContext verifies the integrity of a dump file with the given context.
func (p *Pool) DumpVerifyContext(ctx context.Context, filepath string) (string, error) {
	var out string
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.DumpVerifyContext(ctx, filepath)
		return e
	})
	return out, err
}

// DumpInfo retrieves metadata of a dump file.
func (p *Pool) DumpInfo(filepath string) (string, error) {
	return p.DumpInfoContext(context.Background(), filepath)
}

// DumpInfoContext retrieves metadata of a dump file with the given context.
func (p *Pool) DumpInfoContext(ctx context.Context, filepath string) (string, error) {
	var out string
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.DumpInfoContext(ctx, filepath)
		return e
	})
	return out, err
}

// CacheStats retrieves cache statistics.
func (p *Pool) CacheStats() (*CacheStats, error) {
	return p.CacheStatsContext(context.Background())
}

// CacheStatsContext retrieves cache statistics with the given context.
func (p *Pool) CacheStatsContext(ctx context.Context) (*CacheStats, error) {
	var out *CacheStats
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.CacheStatsContext(ctx)
		return e
	})
	return out, err
}

// ShowVariables shows runtime variables.
func (p *Pool) ShowVariables(likePattern string) (string, error) {
	return p.ShowVariablesContext(context.Background(), likePattern)
}

// ShowVariablesContext is the context-aware variant of ShowVariables.
func (p *Pool) ShowVariablesContext(ctx context.Context, likePattern string) (string, error) {
	var out string
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.ShowVariablesContext(ctx, likePattern)
		return e
	})
	return out, err
}

// SyncStatus retrieves the status of in-flight and recent sync operations.
func (p *Pool) SyncStatus() (string, error) {
	return p.SyncStatusContext(context.Background())
}

// SyncStatusContext is the context-aware variant of SyncStatus.
func (p *Pool) SyncStatusContext(ctx context.Context) (string, error) {
	var out string
	err := p.run(ctx, true, func(c *Client) error {
		var e error
		out, e = c.SyncStatusContext(ctx)
		return e
	})
	return out, err
}

// --- State-changing commands (never retried after reaching the server) ---

// StartReplication starts replication.
func (p *Pool) StartReplication() error {
	return p.StartReplicationContext(context.Background())
}

// StartReplicationContext starts replication with the given context.
func (p *Pool) StartReplicationContext(ctx context.Context) error {
	return p.run(ctx, false, func(c *Client) error {
		return c.StartReplicationContext(ctx)
	})
}

// StopReplication stops replication.
func (p *Pool) StopReplication() error {
	return p.StopReplicationContext(context.Background())
}

// StopReplicationContext stops replication with the given context.
func (p *Pool) StopReplicationContext(ctx context.Context) error {
	return p.run(ctx, false, func(c *Client) error {
		return c.StopReplicationContext(ctx)
	})
}

// EnableDebug enables debug mode.
func (p *Pool) EnableDebug() error {
	return p.EnableDebugContext(context.Background())
}

// EnableDebugContext enables debug mode with the given context.
func (p *Pool) EnableDebugContext(ctx context.Context) error {
	return p.run(ctx, false, func(c *Client) error {
		return c.EnableDebugContext(ctx)
	})
}

// DisableDebug disables debug mode.
func (p *Pool) DisableDebug() error {
	return p.DisableDebugContext(context.Background())
}

// DisableDebugContext disables debug mode with the given context.
func (p *Pool) DisableDebugContext(ctx context.Context) error {
	return p.run(ctx, false, func(c *Client) error {
		return c.DisableDebugContext(ctx)
	})
}

// DumpSave saves the index to a file.
func (p *Pool) DumpSave(filepath string) (string, error) {
	return p.DumpSaveContext(context.Background(), filepath)
}

// DumpSaveContext saves the index to a file with the given context.
func (p *Pool) DumpSaveContext(ctx context.Context, filepath string) (string, error) {
	var out string
	err := p.run(ctx, false, func(c *Client) error {
		var e error
		out, e = c.DumpSaveContext(ctx, filepath)
		return e
	})
	return out, err
}

// DumpSaveAndWait saves the index and blocks until the dump has finished.
func (p *Pool) DumpSaveAndWait(filepath string) (string, error) {
	return p.DumpSaveAndWaitContext(context.Background(), filepath)
}

// DumpSaveAndWaitContext saves the index and blocks until the dump has
// finished. The DUMP STATUS polling runs on the borrowed connection, so the
// slot stays checked out for the whole operation.
func (p *Pool) DumpSaveAndWaitContext(ctx context.Context, filepath string) (string, error) {
	var out string
	err := p.run(ctx, false, func(c *Client) error {
		var e error
		out, e = c.DumpSaveAndWaitContext(ctx, filepath)
		return e
	})
	return out, err
}

// DumpLoad loads the index from a file.
func (p *Pool) DumpLoad(filepath string) error {
	return p.DumpLoadContext(context.Background(), filepath)
}

// DumpLoadContext loads the index from a file with the given context.
func (p *Pool) DumpLoadContext(ctx context.Context, filepath string) error {
	return p.run(ctx, false, func(c *Client) error {
		return c.DumpLoadContext(ctx, filepath)
	})
}

// CacheClear clears the cache.
func (p *Pool) CacheClear(table string) error {
	return p.CacheClearContext(context.Background(), table)
}

// CacheClearContext clears the cache with the given context.
func (p *Pool) CacheClearContext(ctx context.Context, table string) error {
	return p.run(ctx, false, func(c *Client) error {
		return c.CacheClearContext(ctx, table)
	})
}

// CacheEnable enables the cache.
func (p *Pool) CacheEnable() error {
	return p.CacheEnableContext(context.Background())
}

// CacheEnableContext enables the cache with the given context.
func (p *Pool) CacheEnableContext(ctx context.Context) error {
	return p.run(ctx, false, func(c *Client) error {
		return c.CacheEnableContext(ctx)
	})
}

// CacheDisable disables the cache.
func (p *Pool) CacheDisable() error {
	return p.CacheDisableContext(context.Background())
}

// CacheDisableContext disables the cache with the given context.
func (p *Pool) CacheDisableContext(ctx context.Context) error {
	return p.run(ctx, false, func(c *Client) error {
		return c.CacheDisableContext(ctx)
	})
}

// Optimize optimizes the index.
func (p *Pool) Optimize(table string) error {
	return p.OptimizeContext(context.Background(), table)
}

// OptimizeContext optimizes the index with the given context.
func (p *Pool) OptimizeContext(ctx context.Context, table string) error {
	return p.run(ctx, false, func(c *Client) error {
		return c.OptimizeContext(ctx, table)
	})
}

// SetVariable sets a runtime variable.
func (p *Pool) SetVariable(name, value string) error {
	return p.SetVariableContext(context.Background(), name, value)
}

// SetVariableContext sets a runtime variable with the given context.
func (p *Pool) SetVariableContext(ctx context.Context, name, value string) error {
	return p.run(ctx, false, func(c *Client) error {
		return c.SetVariableContext(ctx, name, value)
	})
}

// Sync starts an on-demand sync (full reload) of a table.
func (p *Pool) Sync(table string) (string, error) {
	return p.SyncContext(context.Background(), table)
}

// SyncContext is the context-aware variant of Sync.
func (p *Pool) SyncContext(ctx context.Context, table string) (string, error) {
	var out string
	err := p.run(ctx, false, func(c *Client) error {
		var e error
		out, e = c.SyncContext(ctx, table)
		return e
	})
	return out, err
}

// SyncStop stops a running sync.
func (p *Pool) SyncStop(table string) (string, error) {
	return p.SyncStopContext(context.Background(), table)
}

// SyncStopContext is the context-aware variant of SyncStop.
func (p *Pool) SyncStopContext(ctx context.Context, table string) (string, error) {
	var out string
	err := p.run(ctx, false, func(c *Client) error {
		var e error
		out, e = c.SyncStopContext(ctx, table)
		return e
	})
	return out, err
}
