// Package api wires the medconnect HTTP service: request routing and the
// server lifecycle (start + graceful shutdown). Feature routes are added in
// later slices; Task 0.1 registers only the health check.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// shutdownTimeout bounds how long a graceful shutdown waits for in-flight
// requests to complete before the server is forced closed.
const shutdownTimeout = 10 * time.Second

// NewHandler builds the HTTP handler with all routes registered. It uses the
// method+path pattern support in net/http.ServeMux (Go 1.22+), so no external
// router dependency is required.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	return mux
}

// handleHealthz reports service liveness.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// Serve runs the HTTP server on ln until ctx is cancelled, then shuts it down
// gracefully, draining in-flight requests. It returns nil on a clean shutdown
// and a non-nil error if the server fails to serve.
func Serve(ctx context.Context, ln net.Listener, logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           NewHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	logger.Info("server listening", "addr", ln.Addr().String())

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining in-flight requests")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
