package justcode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWaitHealthyImmediate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/global/health" {
			t.Errorf("path = %q, want /global/health", r.URL.Path)
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != "opencode" || p != "pw" {
			t.Errorf("basic auth = %q:%q (ok=%v), want opencode:pw", u, p, ok)
		}
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}))
	defer srv.Close()

	cfg := HealthConfig{Deadline: time.Second, PollInterval: time.Millisecond, RequestTimeout: time.Second}
	if err := WaitHealthy(context.Background(), srv.URL, "opencode", "pw", cfg, nil); err != nil {
		t.Fatalf("WaitHealthy: %v", err)
	}
}

func TestWaitHealthyEmptyPassword(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, p, ok := r.BasicAuth()
		if !ok || p != "" {
			t.Errorf("password = %q (ok=%v), want empty (no auth)", p, ok)
		}
		_, _ = w.Write([]byte("healthy"))
	}))
	defer srv.Close()

	cfg := HealthConfig{Deadline: time.Second, PollInterval: time.Millisecond, RequestTimeout: time.Second}
	if err := WaitHealthy(context.Background(), srv.URL, "opencode", "", cfg, nil); err != nil {
		t.Fatalf("WaitHealthy: %v", err)
	}
}

func TestWaitHealthyDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"starting"}`)) // never healthy
	}))
	defer srv.Close()

	cfg := HealthConfig{Deadline: 60 * time.Millisecond, PollInterval: 5 * time.Millisecond, RequestTimeout: time.Second}
	start := time.Now()
	err := WaitHealthy(context.Background(), srv.URL, "opencode", "pw", cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "did not become healthy") {
		t.Fatalf("expected deadline error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("deadline not respected: took %v", elapsed)
	}
}

func TestWaitHealthyPerRequestTimeout(t *testing.T) {
	// A handler that hangs longer than RequestTimeout must not stall the wait:
	// each probe times out individually and the wall-clock deadline still wins.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(800 * time.Millisecond)
		_, _ = w.Write([]byte("healthy"))
	}))
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})

	cfg := HealthConfig{Deadline: 150 * time.Millisecond, PollInterval: 10 * time.Millisecond, RequestTimeout: 50 * time.Millisecond}
	start := time.Now()
	err := WaitHealthy(context.Background(), srv.URL, "opencode", "pw", cfg, nil)
	if err == nil {
		t.Fatal("expected an error from a hung endpoint")
	}
	if elapsed := time.Since(start); elapsed > 600*time.Millisecond {
		t.Fatalf("hung request bypassed deadline: took %v", elapsed)
	}
}

func TestWaitHealthyContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("starting"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	cfg := HealthConfig{Deadline: 0, PollInterval: 5 * time.Millisecond, RequestTimeout: time.Second}
	if err := WaitHealthy(ctx, srv.URL, "opencode", "pw", cfg, nil); err == nil {
		t.Fatal("expected context cancellation error")
	}
}
