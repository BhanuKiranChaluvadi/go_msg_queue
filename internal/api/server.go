// Package api wires the medconnect HTTP hub: request routing, cross-cutting
// middleware, and the server lifecycle (start + graceful shutdown). Feature
// routes and dependencies are added to Server in later slices.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"medconnect/internal/appointments"
	"medconnect/internal/domain"
	"medconnect/internal/events"
	"medconnect/internal/platform"
	"medconnect/internal/tenancy"
)

// shutdownTimeout bounds how long a graceful shutdown waits for in-flight
// requests to complete before the server is forced closed.
const shutdownTimeout = 10 * time.Second

// Server holds the hub's dependencies and builds its HTTP handler. It is the
// composition root's view of the service; feature stores and services are added
// as fields in later slices.
type Server struct {
	Logger        *slog.Logger
	IDGen         platform.IDGen
	Publisher     *events.Publisher
	InternalToken string
	Resolver      tenancy.ActorResolver
	Appointments  *appointments.Service
}

// Handler builds the fully-wrapped HTTP handler: routes plus the request-id and
// logging middleware. It uses net/http.ServeMux method+path patterns (Go 1.22+),
// so no router dependency is required.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.Handle("GET /internal/events", s.internalAuth(http.HandlerFunc(s.handleInternalEvents)))

	// authed wraps a handler with tenant/actor authentication for the v1 API.
	authed := func(h http.Handler) http.Handler { return tenancy.Authenticate(s.Resolver)(h) }

	// Appointments — timeslots (Feature 1).
	mux.Handle("POST /v1/timeslots",
		authed(tenancy.RequireRole(domain.RoleDoctor, http.HandlerFunc(s.handleRegisterTimeslot))))
	mux.Handle("GET /v1/doctors/{doctorId}/timeslots",
		authed(http.HandlerFunc(s.handleListDoctorTimeslots)))

	// Appointments — booking (Feature 1).
	mux.Handle("POST /v1/appointments",
		authed(tenancy.RequireRole(domain.RolePatient, http.HandlerFunc(s.handleBookAppointment))))
	mux.Handle("GET /v1/appointments/next",
		authed(tenancy.RequireRole(domain.RoleDoctor, http.HandlerFunc(s.handleNextAppointments))))
	mux.Handle("POST /v1/appointments/{id}/notes",
		authed(tenancy.RequireRole(domain.RoleDoctor, http.HandlerFunc(s.handleAddNote))))
	mux.Handle("POST /v1/appointments/{id}/prescriptions",
		authed(tenancy.RequireRole(domain.RoleDoctor, http.HandlerFunc(s.handleIssuePrescription))))

	return Chain(mux, RequestID(s.IDGen), Logging(s.Logger))
}

// handleHealthz reports service liveness.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// Serve runs h on ln until ctx is cancelled, then shuts down gracefully,
// draining in-flight requests. It returns nil on a clean shutdown and a non-nil
// error if the server fails to serve.
func Serve(ctx context.Context, ln net.Listener, h http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           h,
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
