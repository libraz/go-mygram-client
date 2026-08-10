package mygram

import (
	"strings"
	"testing"
)

// The administrative AUTH handshake (client.go): the token configured through
// ClientConfig.AdminToken is replayed on every connection, including the ones
// a reconnect opens.

func TestAdminTokenAuthenticatesOnConnect(t *testing.T) {
	srv, addr := newAuthServer(t, "OK AUTHENTICATED\r\n", "OK RESULTS 0\r\n")

	cfg := DefaultConfig()
	host, port := splitHostPort(t, addr)
	cfg.Host, cfg.Port = host, port
	cfg.AdminToken = "s3cret"
	c := NewClientWithConfig(cfg)
	defer func() { _ = c.Close() }()

	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := c.Search("articles", "golang", SearchOptions{}); err != nil {
		t.Fatal(err)
	}

	cmds := srv.recorded()
	if len(cmds) != 2 {
		t.Fatalf("recorded %d commands (%v), want 2", len(cmds), cmds)
	}
	if cmds[0] != "AUTH s3cret" {
		t.Errorf("first command = %q, want %q", cmds[0], "AUTH s3cret")
	}
	if !strings.HasPrefix(cmds[1], "SEARCH ") {
		t.Errorf("second command = %q, want the search", cmds[1])
	}
}

func TestAdminTokenWithWhitespaceIsQuoted(t *testing.T) {
	srv, addr := newAuthServer(t, "OK AUTHENTICATED\r\n", "OK RESULTS 0\r\n")

	cfg := DefaultConfig()
	host, port := splitHostPort(t, addr)
	cfg.Host, cfg.Port = host, port
	cfg.AdminToken = "two words"
	c := NewClientWithConfig(cfg)
	defer func() { _ = c.Close() }()

	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if cmds := srv.recorded(); len(cmds) != 1 || cmds[0] != `AUTH "two words"` {
		t.Errorf("recorded %v, want a single quoted AUTH", cmds)
	}
}

func TestRejectedAdminTokenFailsConnect(t *testing.T) {
	_, addr := newAuthServer(t, "ERROR 7 Authentication failed\r\n", "OK RESULTS 0\r\n")

	cfg := DefaultConfig()
	host, port := splitHostPort(t, addr)
	cfg.Host, cfg.Port = host, port
	cfg.AdminToken = "wrong"
	c := NewClientWithConfig(cfg)
	defer func() { _ = c.Close() }()

	err := c.Connect()
	se, ok := err.(*ServerError)
	if !ok {
		t.Fatalf("expected *ServerError, got %T (%v)", err, err)
	}
	if se.Code != ErrCodePermissionDenied {
		t.Errorf("Code = %d, want %d", se.Code, ErrCodePermissionDenied)
	}
	if c.IsConnected() {
		t.Error("client reports itself connected after a rejected token")
	}
}

func TestNoAdminTokenSendsNoAuth(t *testing.T) {
	srv, addr := newAuthServer(t, "OK AUTHENTICATED\r\n", "OK RESULTS 0\r\n")

	host, port := splitHostPort(t, addr)
	cfg := DefaultConfig()
	cfg.Host, cfg.Port = host, port
	c := NewClientWithConfig(cfg)
	defer func() { _ = c.Close() }()

	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search("articles", "golang", SearchOptions{}); err != nil {
		t.Fatal(err)
	}
	cmds := srv.recorded()
	if len(cmds) != 1 || strings.HasPrefix(cmds[0], "AUTH") {
		t.Errorf("recorded %v, want the search alone", cmds)
	}
}

func TestAuthenticateRemembersTokenForReconnect(t *testing.T) {
	srv, addr := newAuthServer(t, "OK AUTHENTICATED\r\n", "OK RESULTS 0\r\n")

	host, port := splitHostPort(t, addr)
	cfg := DefaultConfig()
	cfg.Host, cfg.Port = host, port
	cfg.AutoReconnect = true
	c := NewClientWithConfig(cfg)
	defer func() { _ = c.Close() }()

	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := c.Authenticate("s3cret"); err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	// Drop the socket the way a server-side idle reap would, then issue a
	// command: the reconnect must re-authenticate before sending it.
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search("articles", "golang", SearchOptions{}); err != nil {
		t.Fatal(err)
	}

	cmds := srv.recorded()
	if len(cmds) != 3 {
		t.Fatalf("recorded %d commands (%v), want 3", len(cmds), cmds)
	}
	if cmds[0] != "AUTH s3cret" || cmds[1] != "AUTH s3cret" {
		t.Errorf("recorded %v, want AUTH replayed on the reconnected socket", cmds)
	}
	if srv.connections() != 2 {
		t.Errorf("server accepted %d connections, want 2", srv.connections())
	}
}

func TestAuthenticateRejectsEmptyToken(t *testing.T) {
	c := NewClient("127.0.0.1:1")
	if err := c.Authenticate(""); err == nil {
		t.Fatal("expected an error for an empty token")
	}
}

func TestAuthenticateRejectsUnexpectedAcknowledgement(t *testing.T) {
	c, _ := connectRecording(t, "OK SOMETHING_ELSE\r\n")
	err := c.AuthenticateContext(testContext(t), "s3cret")
	if _, ok := err.(*ProtocolError); !ok {
		t.Fatalf("expected ProtocolError, got %T (%v)", err, err)
	}
}
