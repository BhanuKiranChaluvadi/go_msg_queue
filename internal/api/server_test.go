package api

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"medconnect/internal/events"
	"medconnect/internal/platform"
)

// newTestServer builds a Server with quiet logging and deterministic ids for use
// in api tests.
func newTestServer() *Server {
	idgen := platform.NewFakeIDGen("req-")
	return &Server{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		IDGen:         idgen,
		Publisher:     events.NewPublisher(events.NewStore(), platform.SystemClock{}, idgen),
		InternalToken: "secret",
	}
}

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	newTestServer().Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("body = %q, want it to contain \"status\":\"ok\"", body)
	}
}

func TestServeGracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, newTestServer().Handler(), logger) }()

	// The server should become reachable and answer the health check.
	url := "http://" + ln.Addr().String() + "/healthz"
	resp := getWithRetry(t, url)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Cancelling the context triggers graceful shutdown; Serve returns nil.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil on clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not shut down within timeout")
	}
}

// getWithRetry polls url until the server accepts connections or the attempts
// are exhausted, so the test does not race the server's startup.
func getWithRetry(t *testing.T, url string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for i := 0; i < 50; i++ {
		resp, err := client.Get(url)
		if err == nil {
			return resp
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server never became reachable: %v", lastErr)
	return nil
}
