package mygram

import (
	"bufio"
	"net"
	"sync"
	"testing"
	"time"
)

// eventRecorder collects pool events in a concurrency-safe slice.
type eventRecorder struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	event   PoolEvent
	payload map[string]any
}

func (r *eventRecorder) on(event PoolEvent, payload map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{event, payload})
}

func (r *eventRecorder) count(event PoolEvent) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.event == event {
			n++
		}
	}
	return n
}

func (r *eventRecorder) last(event PoolEvent) (map[string]any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].event == event {
			return r.events[i].payload, true
		}
	}
	return nil, false
}

func TestPoolEmitsAcquire(t *testing.T) {
	addr, _, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	rec := &eventRecorder{}
	p := poolFor(t, addr, PoolConfig{MaxOpenConns: 2, OnEvent: rec.on})
	if _, err := p.Search("threads", "k", SearchOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if rec.count(EventAcquire) == 0 {
		t.Fatal("expected at least one acquire event")
	}
	payload, _ := rec.last(EventAcquire)
	if _, ok := payload["wait_seconds"].(float64); !ok {
		t.Errorf("acquire payload = %v, want a float64 wait_seconds", payload)
	}
}

// dropFirstThenEcho closes the first accepted connection right after reading a
// request (a read-phase drop, which is retried for idempotent commands), and
// echoes on every later connection.
func dropFirstThenEcho(idx int, conn net.Conn) {
	if idx == 1 {
		br := bufio.NewReader(conn)
		_, _ = br.ReadString('\n')
		_ = conn.Close()
		return
	}
	echoSearchServer(idx, conn)
}

func TestPoolEmitsRetryAndDiscard(t *testing.T) {
	addr, _, cleanup := poolMockServer(t, dropFirstThenEcho)
	defer cleanup()

	rec := &eventRecorder{}
	p := poolFor(t, addr, PoolConfig{
		MaxOpenConns: 2,
		Retry:        RetryPolicy{MaxRetries: 3, BaseBackoff: time.Millisecond},
		OnEvent:      rec.on,
	})

	// The idempotent Search survives the first connection's drop by retrying on
	// a fresh connection.
	if _, err := p.Search("threads", "k", SearchOptions{Limit: 1}); err != nil {
		t.Fatalf("Search failed despite retry: %v", err)
	}
	if rec.count(EventRetry) == 0 {
		t.Error("expected a retry event after the connection drop")
	}
	if rec.count(EventConnectionDiscarded) == 0 {
		t.Error("expected a connection_discarded event for the dropped connection")
	}
	payload, ok := rec.last(EventRetry)
	if !ok {
		t.Fatal("no retry event recorded")
	}
	if _, ok := payload["attempt"].(int); !ok {
		t.Errorf("retry payload = %v, want an int attempt", payload)
	}
	if _, ok := payload["error"].(string); !ok {
		t.Errorf("retry payload = %v, want a string error", payload)
	}
}

func TestPoolEmitsBreakerStateChange(t *testing.T) {
	addr := reserveClosedAddr(t)
	host, port := splitHostPort(t, addr)

	rec := &eventRecorder{}
	p := NewPool(PoolConfig{
		ClientConfig:   ClientConfig{Host: host, Port: port, TimeoutMs: 200},
		MaxOpenConns:   1,
		Retry:          RetryPolicy{MaxRetries: 0},
		CircuitBreaker: &CircuitBreakerConfig{FailureThreshold: 2, ResetTimeout: time.Minute},
		OnEvent:        rec.on,
	})
	t.Cleanup(func() { _ = p.Close() })

	for i := 0; i < 2; i++ {
		_, _ = p.Search("threads", "k", SearchOptions{Limit: 1})
	}
	payload, ok := rec.last(EventBreakerStateChange)
	if !ok {
		t.Fatal("expected a breaker_state_change event")
	}
	if payload["state"] != "open" {
		t.Errorf("breaker state = %v, want open", payload["state"])
	}
}

func TestPoolEventCallbackPanicIsContained(t *testing.T) {
	addr, _, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	p := poolFor(t, addr, PoolConfig{
		MaxOpenConns: 2,
		OnEvent:      func(PoolEvent, map[string]any) { panic("instrumentation blew up") },
	})
	resp, err := p.Search("threads", "k", SearchOptions{Limit: 1})
	if err != nil {
		t.Fatalf("a panicking OnEvent callback broke the pool: %v", err)
	}
	if resp.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1", resp.TotalCount)
	}
}
