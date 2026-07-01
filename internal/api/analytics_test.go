package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"medconnect/internal/analytics"
)

func TestAnalytics_Handler(t *testing.T) {
	srv := newTestServer()

	// Two patients book; one prescription is issued.
	appt1 := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2)
	_ = bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a2", 3, 4)
	rxBody := map[string]any{"medication": "Aspirin", "expiresAt": apiTestNow.Add(30 * 24 * time.Hour)}
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+appt1+"/prescriptions", "hosp-A", "doc-a", rxBody); rec.Code != http.StatusCreated {
		t.Fatalf("rx: %d", rec.Code)
	}

	rec := doRequest(t, srv, http.MethodGet, "/v1/analytics", "hosp-A", "doc-a", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("analytics: %d %s", rec.Code, rec.Body.String())
	}
	var sum analytics.Summary
	_ = json.Unmarshal(rec.Body.Bytes(), &sum)
	if sum.TotalAppointments != 2 || sum.ActivePatients != 2 || sum.PrescriptionCounts.Issued != 1 {
		t.Errorf("summary = %+v, want 2 appts / 2 patients / 1 issued", sum)
	}

	// Another tenant sees its own (empty) numbers.
	rec = doRequest(t, srv, http.MethodGet, "/v1/analytics", "hosp-B", "doc-b", nil)
	var other analytics.Summary
	_ = json.Unmarshal(rec.Body.Bytes(), &other)
	if other.TotalAppointments != 0 {
		t.Errorf("hosp-B summary = %+v, want zeros (tenant isolation)", other)
	}
}

func TestAnalytics_Handler_RoleAndAuth(t *testing.T) {
	srv := newTestServer()
	if rec := doRequest(t, srv, http.MethodGet, "/v1/analytics", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth = %d, want 401", rec.Code)
	}
	if rec := doRequest(t, srv, http.MethodGet, "/v1/analytics", "hosp-A", "pat-a", nil); rec.Code != http.StatusForbidden {
		t.Errorf("patient = %d, want 403", rec.Code)
	}
}
