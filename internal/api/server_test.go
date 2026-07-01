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

	"medconnect/internal/appointments"
	"medconnect/internal/domain"
	"medconnect/internal/events"
	"medconnect/internal/platform"
	"medconnect/internal/store/memory"
	"medconnect/internal/tenancy"
	"medconnect/internal/webhooks"
)

// apiTestNow is the fixed clock time used by the wired appointments service in
// api tests, so future-slot timestamps are deterministic.
var apiTestNow = time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)

// apiTestResolver seeds actors across two hospitals for multi-tenant tests.
func apiTestResolver() tenancy.StaticResolver {
	return tenancy.StaticResolver{
		"doc-a":   {ID: "doc-a", TenantID: "hosp-A", Role: domain.RoleDoctor},
		"pat-a":   {ID: "pat-a", TenantID: "hosp-A", Role: domain.RolePatient},
		"pat-a2":  {ID: "pat-a2", TenantID: "hosp-A", Role: domain.RolePatient},
		"pharm-a": {ID: "pharm-a", TenantID: "hosp-A", Role: domain.RolePharmacist},
		"doc-b":   {ID: "doc-b", TenantID: "hosp-B", Role: domain.RoleDoctor},
		"pat-b":   {ID: "pat-b", TenantID: "hosp-B", Role: domain.RolePatient},
	}
}

// newTestServer builds a fully-wired Server with quiet logging, deterministic
// ids, a fixed clock, and a multi-tenant resolver for api tests.
func newTestServer() *Server {
	idgen := platform.NewFakeIDGen("req-")
	clock := platform.NewFakeClock(apiTestNow)
	publisher := events.NewPublisher(events.NewStore(), platform.SystemClock{}, idgen)
	appts := appointments.NewService(appointments.Deps{
		Timeslots:     memory.NewTimeslotStore(),
		Appointments:  memory.NewAppointmentStore(),
		Notes:         memory.NewNoteStore(),
		Prescriptions: memory.NewPrescriptionStore(),
		Clock:         clock,
		IDGen:         platform.NewFakeIDGen("ts-"),
		Events:        publisher,
	})
	return &Server{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		IDGen:         idgen,
		Publisher:     publisher,
		InternalToken: "secret",
		Resolver:      apiTestResolver(),
		Appointments:  appts,
		Webhooks:      webhooks.NewRegistry(memory.NewWebhookStore(), platform.NewFakeIDGen("wh-")),
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
