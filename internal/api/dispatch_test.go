package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"medconnect/internal/domain"
)

func TestListActivePrescriptions_Handler(t *testing.T) {
	srv := newTestServer()

	// A doctor issues a prescription on a booked appointment.
	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2)
	rxBody := map[string]any{"medication": "Aspirin 100mg", "expiresAt": apiTestNow.Add(30 * 24 * time.Hour)}
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/prescriptions", "hosp-A", "doc-a", rxBody); rec.Code != http.StatusCreated {
		t.Fatalf("issue rx: %d %s", rec.Code, rec.Body.String())
	}

	// The pharmacist lists active prescriptions.
	rec := doRequest(t, srv, http.MethodGet, "/v1/prescriptions?status=active", "hosp-A", "pharm-a", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var list []domain.Prescription
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Medication != "Aspirin 100mg" {
		t.Errorf("active list = %+v, want one Aspirin prescription", list)
	}

	// Role + auth checks.
	if rec := doRequest(t, srv, http.MethodGet, "/v1/prescriptions", "hosp-A", "doc-a", nil); rec.Code != http.StatusForbidden {
		t.Errorf("doctor list = %d, want 403", rec.Code)
	}
	if rec := doRequest(t, srv, http.MethodGet, "/v1/prescriptions", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth list = %d, want 401", rec.Code)
	}
}
