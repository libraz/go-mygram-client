package mygram

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Mock servers and client constructors shared by the tests in this package.
// Test infrastructure has no source file of its own, so it lives here rather
// than in whichever test file happened to need it first.

// mockServer starts a mock server for testing and returns the address and cleanup function.
func mockServer(t *testing.T, handler func(conn net.Conn)) (addr string, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go handler(conn)
		}
	}()

	return ln.Addr().String(), func() { _ = ln.Close() }
}

// recordingServer captures the last command received from the client. Since
// clients reuse the same connection and the server goroutine may outlive the
// call, guard access with a mutex.
type recordingServer struct {
	mu       sync.Mutex
	lastCmd  string
	response string
}

func (s *recordingServer) lastCommand() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCmd
}

// newRecordingServer spins up a mock TCP server that records the command it
// receives and replies with the caller-supplied response.
func newRecordingServer(t *testing.T, initialResponse string) (*recordingServer, string, func()) {
	t.Helper()
	s := &recordingServer{response: initialResponse}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 4096)
				n, readErr := c.Read(buf)
				if readErr != nil {
					return
				}
				cmd := strings.TrimRight(string(buf[:n]), "\r\n")
				s.mu.Lock()
				s.lastCmd = cmd
				resp := s.response
				s.mu.Unlock()
				_, _ = c.Write([]byte(resp))
			}(conn)
		}
	}()

	return s, ln.Addr().String(), func() { _ = ln.Close() }
}

// connectRecording wires a client to a recording mock server and connects it.
func connectRecording(t *testing.T, response string) (*Client, *recordingServer) {
	t.Helper()
	srv, addr, cleanup := newRecordingServer(t, response)
	t.Cleanup(cleanup)
	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return c, srv
}

// authServer is a mock that keeps the connection open across commands, so the
// AUTH handshake and the command that follows it share one socket.
type authServer struct {
	mu       sync.Mutex
	commands []string
	conns    int

	// authResponse is the reply to AUTH; everything else gets replyToCommand.
	authResponse   string
	replyToCommand string
}

func (s *authServer) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

func (s *authServer) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

func newAuthServer(t *testing.T, authResponse, replyToCommand string) (*authServer, string) {
	t.Helper()
	s := &authServer{authResponse: authResponse, replyToCommand: replyToCommand}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			s.mu.Lock()
			s.conns++
			s.mu.Unlock()
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 4096)
				for {
					n, readErr := c.Read(buf)
					if readErr != nil {
						return
					}
					cmd := strings.TrimRight(string(buf[:n]), "\r\n")
					s.mu.Lock()
					s.commands = append(s.commands, cmd)
					reply := s.replyToCommand
					if strings.HasPrefix(cmd, "AUTH") {
						reply = s.authResponse
					}
					s.mu.Unlock()
					if _, writeErr := c.Write([]byte(reply)); writeErr != nil {
						return
					}
				}
			}(conn)
		}
	}()

	return s, ln.Addr().String()
}

// scriptedServer keeps one connection open across commands and answers each one
// from a queue of replies, so a poll loop can be driven through several states.
type scriptedServer struct {
	mu       sync.Mutex
	commands []string
	replies  []string
	fallback string
}

func (s *scriptedServer) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

// newScriptedServer answers the i-th command with replies[i], and every command
// past the end of the script with fallback.
func newScriptedServer(t *testing.T, fallback string, replies ...string) (*scriptedServer, string) {
	t.Helper()
	s := &scriptedServer{replies: replies, fallback: fallback}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 4096)
				for {
					n, readErr := c.Read(buf)
					if readErr != nil {
						return
					}
					s.mu.Lock()
					idx := len(s.commands)
					s.commands = append(s.commands, strings.TrimRight(string(buf[:n]), "\r\n"))
					reply := s.fallback
					if idx < len(s.replies) {
						reply = s.replies[idx]
					}
					s.mu.Unlock()
					if _, writeErr := c.Write([]byte(reply)); writeErr != nil {
						return
					}
				}
			}(conn)
		}
	}()

	return s, ln.Addr().String()
}

// connectScripted dials a scriptedServer with the supplied configuration.
func connectScripted(t *testing.T, cfg ClientConfig, addr string) *Client {
	t.Helper()
	host, port := splitHostPort(t, addr)
	cfg.Host, cfg.Port = host, port
	c := NewClientWithConfig(cfg)
	t.Cleanup(func() { _ = c.Close() })
	if err := c.ConnectContext(testContext(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return c
}

func clientFor(t *testing.T, addr string, cfg ClientConfig) *Client {
	t.Helper()
	cfg.Host, cfg.Port = splitHostPort(t, addr)
	cfg.MaxQueryLength = 0
	c := NewClientWithConfig(cfg)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// holdServer reads one request and then holds the connection open without
// replying, so the client's read deadline elapses (a read-phase failure).
func holdServer(idx int, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	if _, err := br.ReadString('\n'); err != nil {
		return
	}
	time.Sleep(time.Second) // outlast the client's command timeout
}

// silentServer accepts a connection and reads the command but never replies,
// so the client's read blocks until its deadline fires.
func silentServer(t *testing.T) string {
	t.Helper()
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		// Hold the connection open without responding.
		time.Sleep(2 * time.Second)
		_ = conn.Close()
	})
	t.Cleanup(cleanup)
	return addr
}

// poolMockServer starts a listener whose accepted connections are handled by
// handle. It returns the address, an accessor for the number of accepted
// connections, and a cleanup function.
func poolMockServer(t *testing.T, handle func(idx int, conn net.Conn)) (addr string, accepts func() int, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			idx := int(atomic.AddInt64(&n, 1))
			go handle(idx, conn)
		}
	}()
	return ln.Addr().String(), func() int { return int(atomic.LoadInt64(&n)) }, func() { _ = ln.Close() }
}

func poolFor(t *testing.T, addr string, cfg PoolConfig) *Pool {
	t.Helper()
	cfg.Host, cfg.Port = splitHostPort(t, addr)
	cfg.MaxQueryLength = 0
	p := NewPool(cfg)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// waitAccepts blocks until the mock server's accept count reaches want, failing
// the test if it overshoots or does not arrive within the deadline. The count
// is incremented in the server's accept loop, which runs after the client's
// dial has returned, so reading it immediately would race; polling closes that
// window without masking a genuine extra connection (which pushes the count
// past want and fails fast).
func waitAccepts(t *testing.T, accepts func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := accepts()
		if got == want {
			return
		}
		if got > want {
			t.Fatalf("accepted %d connections, want %d", got, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf("accepted %d connections, want %d (timed out)", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// echoSearchServer serves any number of SEARCH-like requests on one connection,
// replying with a fixed single-result response, until the peer closes.
func echoSearchServer(idx int, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	for {
		if _, err := br.ReadString('\n'); err != nil {
			return
		}
		if _, err := conn.Write([]byte("OK RESULTS 1 42\r\n")); err != nil {
			return
		}
	}
}

// dropFirstThenServe closes the first accepted connection without responding
// (simulating a dropped/stale connection) and serves later connections
// normally.
func dropFirstThenServe(idx int, conn net.Conn) {
	br := bufio.NewReader(conn)
	if _, err := br.ReadString('\n'); err != nil {
		_ = conn.Close()
		return
	}
	if idx == 1 {
		_ = conn.Close() // drop after reading, before responding
		return
	}
	_, _ = conn.Write([]byte("OK RESULTS 1 42\r\n"))
	echoSearchServer(idx, conn)
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	c := NewClient(addr)
	return c.config.Host, c.config.Port
}

// testContext returns a context bounded so a hung mock cannot stall the suite.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}
