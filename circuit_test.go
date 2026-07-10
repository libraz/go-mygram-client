package mygram

import (
	"net"
	"sync"
	"testing"
	"time"
)

func TestIsNetworkFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"connection", &ConnectionError{Message: "x"}, true},
		{"timeout", &TimeoutError{Message: "x"}, true},
		{"server", &ServerError{Message: "x"}, false},
		{"validation", &ValidationError{Message: "x"}, false},
		{"protocol", &ProtocolError{Message: "x"}, false},
		{"circuit-open", &CircuitOpenError{Message: "x"}, false},
		{"pool-closed", errPoolClosedSentinel, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNetworkFailure(tc.err); got != tc.want {
				t.Errorf("isNetworkFailure(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestCircuitBreakerDefaults(t *testing.T) {
	b := newCircuitBreaker(CircuitBreakerConfig{}, nil)
	if b.failureThreshold != defaultFailureThreshold {
		t.Errorf("failureThreshold = %d, want %d", b.failureThreshold, defaultFailureThreshold)
	}
	if b.resetTimeout != defaultResetTimeout {
		t.Errorf("resetTimeout = %v, want %v", b.resetTimeout, defaultResetTimeout)
	}
	if b.state != "closed" {
		t.Errorf("initial state = %q, want closed", b.state)
	}
}

func TestCircuitBreakerOpensAfterThreshold(t *testing.T) {
	var states []string
	b := newCircuitBreaker(
		CircuitBreakerConfig{FailureThreshold: 3, ResetTimeout: time.Minute},
		func(s string) { states = append(states, s) },
	)

	// Two failures stay closed.
	for i := 0; i < 2; i++ {
		if err := b.beforeRequest(); err != nil {
			t.Fatalf("beforeRequest unexpectedly denied: %v", err)
		}
		b.record(&ConnectionError{Message: "boom"})
	}
	if b.state != "closed" {
		t.Fatalf("state = %q after 2 failures, want closed", b.state)
	}

	// Third failure trips it.
	if err := b.beforeRequest(); err != nil {
		t.Fatalf("beforeRequest denied before trip: %v", err)
	}
	b.record(&ConnectionError{Message: "boom"})
	if b.state != "open" {
		t.Fatalf("state = %q after threshold, want open", b.state)
	}

	// Open now fast-fails with CircuitOpenError.
	if err := b.beforeRequest(); err == nil {
		t.Fatal("beforeRequest allowed a request while open")
	} else if _, ok := err.(*CircuitOpenError); !ok {
		t.Fatalf("err = %T, want *CircuitOpenError", err)
	}

	if len(states) != 1 || states[0] != "open" {
		t.Errorf("state transitions = %v, want [open]", states)
	}
}

func TestCircuitBreakerSuccessResetsCounter(t *testing.T) {
	b := newCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 3}, nil)
	b.record(&ConnectionError{Message: "boom"})
	b.record(&ConnectionError{Message: "boom"})
	// A success resets the consecutive-failure count.
	b.record(nil)
	b.record(&ConnectionError{Message: "boom"})
	b.record(&ConnectionError{Message: "boom"})
	if b.state != "closed" {
		t.Errorf("state = %q, want closed (success should have reset the counter)", b.state)
	}
}

func TestCircuitBreakerServerErrorDoesNotTrip(t *testing.T) {
	b := newCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 2}, nil)
	for i := 0; i < 5; i++ {
		b.record(&ServerError{Message: "rejected"})
	}
	if b.state != "closed" {
		t.Errorf("state = %q, want closed (server errors must not trip the breaker)", b.state)
	}
}

func TestCircuitBreakerHalfOpenTrial(t *testing.T) {
	var states []string
	b := newCircuitBreaker(
		CircuitBreakerConfig{FailureThreshold: 1, ResetTimeout: 10 * time.Millisecond},
		func(s string) { states = append(states, s) },
	)

	// Trip it.
	b.record(&ConnectionError{Message: "boom"})
	if b.state != "open" {
		t.Fatalf("state = %q, want open", b.state)
	}

	// Before the reset timeout, requests are denied.
	if err := b.beforeRequest(); err == nil {
		t.Fatal("beforeRequest allowed a request before reset timeout")
	}

	time.Sleep(15 * time.Millisecond)

	// The first probe after the timeout is admitted (half-open).
	if err := b.beforeRequest(); err != nil {
		t.Fatalf("half-open probe denied: %v", err)
	}
	if b.state != "half_open" {
		t.Fatalf("state = %q, want half_open", b.state)
	}
	// A concurrent second probe is rejected while the trial is in flight.
	if err := b.beforeRequest(); err == nil {
		t.Fatal("beforeRequest admitted a second half-open trial")
	}

	// The trial fails → reopen.
	b.record(&ConnectionError{Message: "still down"})
	if b.state != "open" {
		t.Fatalf("state = %q, want open after failed trial", b.state)
	}

	// Wait, probe again, and let the trial succeed → closed.
	time.Sleep(15 * time.Millisecond)
	if err := b.beforeRequest(); err != nil {
		t.Fatalf("half-open probe denied: %v", err)
	}
	b.record(nil)
	if b.state != "closed" {
		t.Fatalf("state = %q, want closed after successful trial", b.state)
	}

	// Transitions: open, half_open, open, half_open, closed.
	want := []string{"open", "half_open", "open", "half_open", "closed"}
	if len(states) != len(want) {
		t.Fatalf("transitions = %v, want %v", states, want)
	}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("transitions = %v, want %v", states, want)
		}
	}
}

func TestPoolBreakerFastFailsWhenOpen(t *testing.T) {
	// A permanently unavailable server: every dial is refused, so the breaker
	// trips and subsequent calls fail fast with CircuitOpenError.
	addr := reserveClosedAddr(t)
	host, port := splitHostPort(t, addr)

	p := NewPool(PoolConfig{
		ClientConfig:   ClientConfig{Host: host, Port: port, TimeoutMs: 200},
		MaxOpenConns:   1,
		Retry:          RetryPolicy{MaxRetries: 0, BaseBackoff: time.Millisecond},
		CircuitBreaker: &CircuitBreakerConfig{FailureThreshold: 3, ResetTimeout: time.Minute},
	})
	t.Cleanup(func() { _ = p.Close() })

	// Drive three connection failures to trip the breaker.
	for i := 0; i < 3; i++ {
		if _, e := p.Search("threads", "k", SearchOptions{Limit: 1}); e == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", i)
		}
	}
	// The next call must fail fast with CircuitOpenError, not another dial error.
	_, e := p.Search("threads", "k", SearchOptions{Limit: 1})
	if _, ok := e.(*CircuitOpenError); !ok {
		t.Fatalf("err = %T (%v), want *CircuitOpenError", e, e)
	}
}

func TestPoolBreakerRecoversAfterHalfOpenSuccess(t *testing.T) {
	// Reserve an address and release it, so dials are refused until a real
	// server is later started on the same address.
	ln0, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln0.Addr().String()
	_ = ln0.Close()
	host, port := splitHostPort(t, addr)

	var mu sync.Mutex
	var states []string
	p := NewPool(PoolConfig{
		ClientConfig:   ClientConfig{Host: host, Port: port, TimeoutMs: 200},
		MaxOpenConns:   1,
		Retry:          RetryPolicy{MaxRetries: 0, BaseBackoff: time.Millisecond},
		CircuitBreaker: &CircuitBreakerConfig{FailureThreshold: 2, ResetTimeout: 100 * time.Millisecond},
		OnEvent: func(ev PoolEvent, payload map[string]any) {
			if ev == EventBreakerStateChange {
				mu.Lock()
				states = append(states, payload["state"].(string))
				mu.Unlock()
			}
		},
	})
	t.Cleanup(func() { _ = p.Close() })

	// Two refused dials trip the breaker.
	for i := 0; i < 2; i++ {
		if _, e := p.Search("threads", "k", SearchOptions{Limit: 1}); e == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", i)
		}
	}
	// While open (within ResetTimeout) the breaker fast-fails without dialing.
	if _, e := p.Search("threads", "k", SearchOptions{Limit: 1}); e != nil {
		if _, ok := e.(*CircuitOpenError); !ok {
			t.Fatalf("err = %T (%v), want *CircuitOpenError while open", e, e)
		}
	} else {
		t.Fatal("expected CircuitOpenError while the breaker is open")
	}

	// Bring a real server up on the same address.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("re-listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go echoSearchServer(0, conn)
		}
	}()

	// After the reset timeout, the next request is admitted as a half-open trial;
	// it now reaches the live server, so the breaker closes.
	time.Sleep(140 * time.Millisecond)
	resp, err := p.Search("threads", "k", SearchOptions{Limit: 1})
	if err != nil {
		t.Fatalf("half-open trial should have succeeded against the live server: %v", err)
	}
	if resp.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1", resp.TotalCount)
	}
	// A follow-up confirms the breaker is fully closed and no longer probing.
	if _, err := p.Search("threads", "k", SearchOptions{Limit: 1}); err != nil {
		t.Fatalf("follow-up after recovery failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(states) < 2 || states[0] != "open" || states[len(states)-1] != "closed" {
		t.Fatalf("state transitions = %v, want to start at open and end at closed", states)
	}
	sawHalfOpen := false
	for _, s := range states {
		if s == "half_open" {
			sawHalfOpen = true
		}
	}
	if !sawHalfOpen {
		t.Errorf("state transitions = %v, want a half_open probe before recovery", states)
	}
}

// reserveClosedAddr reserves a TCP address and immediately releases it, so any
// dial to it is refused (connection refused). It returns the address string.
func reserveClosedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}
