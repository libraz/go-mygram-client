// Package mygram is a Go client library for the MygramDB full-text search engine.
//
// MygramDB is an in-memory full-text search engine using a memcached-style text protocol,
// providing 25-200x faster search performance compared to MySQL FULLTEXT.
//
// Basic usage:
//
//	client := mygram.NewClient("localhost:11016")
//	defer client.Close()
//
//	if err := client.Connect(); err != nil {
//	    log.Fatal(err)
//	}
//
//	resp, err := client.Search("threads", "keyword", mygram.SearchOptions{
//	    Limit:      100,
//	    SortColumn: "created_at",
//	    SortDesc:   true,
//	})
package mygram

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client is a MygramDB client.
type Client struct {
	config ClientConfig
	conn   net.Conn
	mu     sync.Mutex
}

// NewClient creates a new client for the given address.
// addr should be in "host:port" format or a Unix socket path.
func NewClient(addr string) *Client {
	config := DefaultConfig()

	// Parse host:port format
	if host, portStr, err := net.SplitHostPort(addr); err == nil {
		config.Host = host
		if port, err := strconv.Atoi(portStr); err == nil {
			config.Port = port
		}
	} else {
		// Possibly a Unix socket path
		config.SocketPath = addr
	}

	return &Client{config: config}
}

// NewClientWithConfig creates a new client with the given configuration.
func NewClientWithConfig(config ClientConfig) *Client {
	defaults := DefaultConfig()
	if config.Host == "" {
		config.Host = defaults.Host
	}
	if config.Port == 0 {
		config.Port = defaults.Port
	}
	if config.TimeoutMs == 0 {
		config.TimeoutMs = defaults.TimeoutMs
	}
	if config.RecvBufferSize == 0 {
		config.RecvBufferSize = defaults.RecvBufferSize
	}
	if config.MaxQueryLength == 0 {
		config.MaxQueryLength = defaults.MaxQueryLength
	}
	return &Client{config: config}
}

// Connect establishes a connection to the server.
func (c *Client) Connect() error {
	return c.ConnectContext(context.Background())
}

// ConnectContext establishes a connection to the server with the given context.
func (c *Client) ConnectContext(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil
	}

	timeout := time.Duration(c.config.TimeoutMs) * time.Millisecond
	var d net.Dialer
	d.Timeout = timeout

	var conn net.Conn
	var err error

	if c.config.SocketPath != "" {
		conn, err = d.DialContext(ctx, "unix", c.config.SocketPath)
	} else {
		addr := net.JoinHostPort(c.config.Host, strconv.Itoa(c.config.Port))
		conn, err = d.DialContext(ctx, "tcp", addr)
	}

	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return &TimeoutError{Message: "connection timeout"}
		}
		return &ConnectionError{Message: "failed to connect", Err: err}
	}

	c.conn = conn
	return nil
}

// Close closes the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// IsConnected returns whether the client is currently connected.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// SendCommand sends a raw command to the server and returns the response.
func (c *Client) SendCommand(command string) (string, error) {
	return c.SendCommandContext(context.Background(), command)
}

// SendCommandContext sends a raw command to the server with the given context.
func (c *Client) SendCommandContext(ctx context.Context, command string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return "", &ConnectionError{Message: "not connected to server"}
	}

	timeout := time.Duration(c.config.TimeoutMs) * time.Millisecond

	// Use context deadline if it is shorter
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d < timeout {
			timeout = d
		}
	}

	// Write
	if err := c.conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return "", &ConnectionError{Message: "failed to set write deadline", Err: err}
	}

	_, err := c.conn.Write([]byte(command + "\r\n"))
	if err != nil {
		c.conn = nil
		return "", &ConnectionError{Message: "failed to send command", Err: err}
	}

	// Read
	response, err := c.readResponse(timeout)
	if err != nil {
		return "", err
	}

	// Normalize CRLF and trim
	response = strings.ReplaceAll(response, "\r\n", "\n")
	response = strings.TrimSpace(response)

	// Check for server error
	if strings.HasPrefix(response, "ERROR ") {
		return "", &ServerError{Message: response[6:]}
	}

	return response, nil
}

// readResponse reads a complete response from the server.
func (c *Client) readResponse(timeout time.Duration) (string, error) {
	buf := make([]byte, c.config.RecvBufferSize)
	var sb strings.Builder
	deadline := time.Now().Add(timeout)

	for {
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			return "", &ConnectionError{Message: "failed to set read deadline", Err: err}
		}

		n, err := c.conn.Read(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return "", &TimeoutError{Message: "command timeout"}
			}
			c.conn = nil
			return "", &ConnectionError{Message: "failed to read response", Err: err}
		}

		sb.Write(buf[:n])
		data := sb.String()

		if isResponseComplete(data) {
			return data, nil
		}
	}
}

// isResponseComplete determines whether the response has been fully received.
func isResponseComplete(data string) bool {
	// Multi-line response (terminated by empty line)
	if strings.HasSuffix(data, "\n\n") || strings.HasSuffix(data, "\r\n\r\n") || strings.HasSuffix(data, "\n\r\n") {
		return true
	}

	// Multi-line INFO/CONFIG response
	if strings.Contains(data, "OK INFO\n") || strings.Contains(data, "OK INFO\r\n") ||
		strings.Contains(data, "OK CONFIG\n") || strings.Contains(data, "OK CONFIG\r\n") ||
		strings.HasPrefix(data, "+OK\n") || strings.HasPrefix(data, "+OK\r\n") ||
		strings.Contains(data, "OK DUMP_STATUS\n") || strings.Contains(data, "OK DUMP_STATUS\r\n") ||
		strings.Contains(data, "OK CACHE_STATS\n") || strings.Contains(data, "OK CACHE_STATS\r\n") {
		return strings.HasSuffix(data, "\n\n") || strings.HasSuffix(data, "\r\n\r\n") || strings.HasSuffix(data, "\n\r\n")
	}

	// REPLICATION response (END marker)
	normalized := strings.ReplaceAll(data, "\r\n", "\n")
	if strings.HasPrefix(normalized, "OK REPLICATION\n") {
		return strings.Contains(normalized, "\nEND\n") || strings.HasSuffix(normalized, "\nEND")
	}

	// DEBUG response
	if strings.Contains(data, "# DEBUG") {
		return strings.HasSuffix(data, "\n\n") || strings.HasSuffix(data, "\r\n\r\n") || strings.HasSuffix(data, "\n\r\n")
	}

	// Single-line response
	lines := strings.Split(data, "\n")
	if len(lines) >= 2 && lines[len(lines)-1] == "" {
		return true
	}
	if strings.HasSuffix(data, "\r\n") && !strings.Contains(data, "\n\n") {
		// Single line with CRLF termination
		first := strings.TrimRight(data, "\r\n")
		if !strings.Contains(first, "\n") {
			return true
		}
	}

	return false
}

// Search performs a full-text search.
func (c *Client) Search(table, query string, opts SearchOptions) (*SearchResponse, error) {
	return c.SearchContext(context.Background(), table, query, opts)
}

// SearchContext performs a full-text search with the given context.
func (c *Client) SearchContext(ctx context.Context, table, query string, opts SearchOptions) (*SearchResponse, error) {
	if err := validateTableName(table); err != nil {
		return nil, err
	}
	if err := ensureSafeValue(query, "query"); err != nil {
		return nil, err
	}
	if err := ensureSafeValues(opts.AndTerms, "andTerms"); err != nil {
		return nil, err
	}
	if err := ensureSafeValues(opts.NotTerms, "notTerms"); err != nil {
		return nil, err
	}
	if err := ensureSafeFilters(opts.Filters); err != nil {
		return nil, err
	}
	if opts.SortColumn != "" {
		if err := ensureSafeValue(opts.SortColumn, "sortColumn"); err != nil {
			return nil, err
		}
	}
	if err := ensureQueryLengthWithinLimit(query, opts.AndTerms, opts.NotTerms, opts.Filters, opts.SortColumn, c.config.MaxQueryLength); err != nil {
		return nil, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 1000
	}

	parts := []string{"SEARCH", table, query}

	for _, term := range opts.AndTerms {
		parts = append(parts, "AND", term)
	}
	for _, term := range opts.NotTerms {
		parts = append(parts, "NOT", term)
	}
	for k, v := range opts.Filters {
		parts = append(parts, "FILTER", k, "=", v)
	}
	if opts.SortColumn != "" {
		dir := "ASC"
		if opts.SortDesc {
			dir = "DESC"
		}
		parts = append(parts, "SORT", opts.SortColumn, dir)
	}

	if opts.Offset > 0 {
		parts = append(parts, "LIMIT", fmt.Sprintf("%d,%d", opts.Offset, limit))
	} else {
		parts = append(parts, "LIMIT", strconv.Itoa(limit))
	}

	response, err := c.SendCommandContext(ctx, strings.Join(parts, " "))
	if err != nil {
		return nil, err
	}
	return parseSearchResponse(response)
}

// Count returns the number of matching documents.
func (c *Client) Count(table, query string, opts CountOptions) (*CountResponse, error) {
	return c.CountContext(context.Background(), table, query, opts)
}

// CountContext performs a count query with the given context.
func (c *Client) CountContext(ctx context.Context, table, query string, opts CountOptions) (*CountResponse, error) {
	if err := validateTableName(table); err != nil {
		return nil, err
	}
	if err := ensureSafeValue(query, "query"); err != nil {
		return nil, err
	}
	if err := ensureSafeValues(opts.AndTerms, "andTerms"); err != nil {
		return nil, err
	}
	if err := ensureSafeValues(opts.NotTerms, "notTerms"); err != nil {
		return nil, err
	}
	if err := ensureSafeFilters(opts.Filters); err != nil {
		return nil, err
	}

	parts := []string{"COUNT", table, query}
	for _, term := range opts.AndTerms {
		parts = append(parts, "AND", term)
	}
	for _, term := range opts.NotTerms {
		parts = append(parts, "NOT", term)
	}
	for k, v := range opts.Filters {
		parts = append(parts, "FILTER", k, "=", v)
	}

	response, err := c.SendCommandContext(ctx, strings.Join(parts, " "))
	if err != nil {
		return nil, err
	}
	return parseCountResponse(response)
}

// Get retrieves a document by its primary key.
func (c *Client) Get(table, primaryKey string) (*Document, error) {
	return c.GetContext(context.Background(), table, primaryKey)
}

// GetContext retrieves a document by its primary key with the given context.
func (c *Client) GetContext(ctx context.Context, table, primaryKey string) (*Document, error) {
	if err := validateTableName(table); err != nil {
		return nil, err
	}
	if err := validatePrimaryKey(primaryKey); err != nil {
		return nil, err
	}

	response, err := c.SendCommandContext(ctx, fmt.Sprintf("GET %s %s", table, primaryKey))
	if err != nil {
		return nil, err
	}
	return parseGetResponse(response)
}

// Info retrieves server information.
func (c *Client) Info() (*ServerInfo, error) {
	return c.InfoContext(context.Background())
}

// InfoContext retrieves server information with the given context.
func (c *Client) InfoContext(ctx context.Context) (*ServerInfo, error) {
	response, err := c.SendCommandContext(ctx, "INFO")
	if err != nil {
		return nil, err
	}
	return parseInfoResponse(response)
}

// GetConfig retrieves server configuration in YAML format.
func (c *Client) GetConfig() (string, error) {
	return c.GetConfigContext(context.Background())
}

// GetConfigContext retrieves server configuration with the given context.
func (c *Client) GetConfigContext(ctx context.Context) (string, error) {
	return c.SendCommandContext(ctx, "CONFIG")
}

// GetReplicationStatus retrieves the replication status.
func (c *Client) GetReplicationStatus() (*ReplicationStatus, error) {
	return c.GetReplicationStatusContext(context.Background())
}

// GetReplicationStatusContext retrieves the replication status with the given context.
func (c *Client) GetReplicationStatusContext(ctx context.Context) (*ReplicationStatus, error) {
	response, err := c.SendCommandContext(ctx, "REPLICATION STATUS")
	if err != nil {
		return nil, err
	}
	return parseReplicationStatusResponse(response)
}

// StartReplication starts replication.
func (c *Client) StartReplication() error {
	return c.StartReplicationContext(context.Background())
}

// StartReplicationContext starts replication with the given context.
func (c *Client) StartReplicationContext(ctx context.Context) error {
	_, err := c.SendCommandContext(ctx, "REPLICATION START")
	return err
}

// StopReplication stops replication.
func (c *Client) StopReplication() error {
	return c.StopReplicationContext(context.Background())
}

// StopReplicationContext stops replication with the given context.
func (c *Client) StopReplicationContext(ctx context.Context) error {
	_, err := c.SendCommandContext(ctx, "REPLICATION STOP")
	return err
}

// EnableDebug enables debug mode.
func (c *Client) EnableDebug() error {
	return c.EnableDebugContext(context.Background())
}

// EnableDebugContext enables debug mode with the given context.
func (c *Client) EnableDebugContext(ctx context.Context) error {
	_, err := c.SendCommandContext(ctx, "DEBUG ON")
	return err
}

// DisableDebug disables debug mode.
func (c *Client) DisableDebug() error {
	return c.DisableDebugContext(context.Background())
}

// DisableDebugContext disables debug mode with the given context.
func (c *Client) DisableDebugContext(ctx context.Context) error {
	_, err := c.SendCommandContext(ctx, "DEBUG OFF")
	return err
}

// DumpSave saves the index to a file.
func (c *Client) DumpSave(filepath string) (string, error) {
	return c.DumpSaveContext(context.Background(), filepath)
}

// DumpSaveContext saves the index to a file with the given context.
func (c *Client) DumpSaveContext(ctx context.Context, filepath string) (string, error) {
	cmd := "DUMP SAVE"
	if filepath != "" {
		cmd += " " + filepath
	}
	return c.SendCommandContext(ctx, cmd)
}

// DumpLoad loads the index from a file.
func (c *Client) DumpLoad(filepath string) error {
	return c.DumpLoadContext(context.Background(), filepath)
}

// DumpLoadContext loads the index from a file with the given context.
func (c *Client) DumpLoadContext(ctx context.Context, filepath string) error {
	cmd := "DUMP LOAD"
	if filepath != "" {
		cmd += " " + filepath
	}
	_, err := c.SendCommandContext(ctx, cmd)
	return err
}

// DumpStatus retrieves the status of a dump operation.
func (c *Client) DumpStatus() (*DumpStatus, error) {
	return c.DumpStatusContext(context.Background())
}

// DumpStatusContext retrieves the status of a dump operation with the given context.
func (c *Client) DumpStatusContext(ctx context.Context) (*DumpStatus, error) {
	response, err := c.SendCommandContext(ctx, "DUMP STATUS")
	if err != nil {
		return nil, err
	}
	return parseDumpStatusResponse(response)
}

// DumpVerify verifies the integrity of a dump file.
func (c *Client) DumpVerify(filepath string) (string, error) {
	return c.DumpVerifyContext(context.Background(), filepath)
}

// DumpVerifyContext verifies the integrity of a dump file with the given context.
func (c *Client) DumpVerifyContext(ctx context.Context, filepath string) (string, error) {
	return c.SendCommandContext(ctx, "DUMP VERIFY "+filepath)
}

// DumpInfo retrieves metadata of a dump file.
func (c *Client) DumpInfo(filepath string) (string, error) {
	return c.DumpInfoContext(context.Background(), filepath)
}

// DumpInfoContext retrieves metadata of a dump file with the given context.
func (c *Client) DumpInfoContext(ctx context.Context, filepath string) (string, error) {
	return c.SendCommandContext(ctx, "DUMP INFO "+filepath)
}

// CacheStats retrieves cache statistics.
func (c *Client) CacheStats() (*CacheStats, error) {
	return c.CacheStatsContext(context.Background())
}

// CacheStatsContext retrieves cache statistics with the given context.
func (c *Client) CacheStatsContext(ctx context.Context) (*CacheStats, error) {
	response, err := c.SendCommandContext(ctx, "CACHE STATS")
	if err != nil {
		return nil, err
	}
	return parseCacheStatsResponse(response)
}

// CacheClear clears the cache. If table is specified, only that table's cache is cleared.
func (c *Client) CacheClear(table string) error {
	return c.CacheClearContext(context.Background(), table)
}

// CacheClearContext clears the cache with the given context.
func (c *Client) CacheClearContext(ctx context.Context, table string) error {
	cmd := "CACHE CLEAR"
	if table != "" {
		cmd += " " + table
	}
	_, err := c.SendCommandContext(ctx, cmd)
	return err
}

// CacheEnable enables the cache.
func (c *Client) CacheEnable() error {
	return c.CacheEnableContext(context.Background())
}

// CacheEnableContext enables the cache with the given context.
func (c *Client) CacheEnableContext(ctx context.Context) error {
	_, err := c.SendCommandContext(ctx, "CACHE ENABLE")
	return err
}

// CacheDisable disables the cache.
func (c *Client) CacheDisable() error {
	return c.CacheDisableContext(context.Background())
}

// CacheDisableContext disables the cache with the given context.
func (c *Client) CacheDisableContext(ctx context.Context) error {
	_, err := c.SendCommandContext(ctx, "CACHE DISABLE")
	return err
}

// Optimize optimizes the index. If table is specified, only that table's index is optimized.
func (c *Client) Optimize(table string) error {
	return c.OptimizeContext(context.Background(), table)
}

// OptimizeContext optimizes the index with the given context.
func (c *Client) OptimizeContext(ctx context.Context, table string) error {
	cmd := "OPTIMIZE"
	if table != "" {
		cmd += " " + table
	}
	_, err := c.SendCommandContext(ctx, cmd)
	return err
}
