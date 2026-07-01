package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"medconnect/internal/clinical"
)

func TestPatientOverview_Handler(t *testing.T) {
	srv := newTestServer()

	// Build some history for pat-a: an appointment with a note, a prescription,
	// and a diagnosis.
	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2)
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/notes", "hosp-A", "doc-a", map[string]string{"text": "Reports headache."}); rec.Code != http.StatusCreated {
		t.Fatalf("note: %d", rec.Code)
	}
	rxBody := map[string]any{"medication": "Aspirin 100mg", "expiresAt": apiTestNow.Add(30 * 24 * time.Hour)}
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/prescriptions", "hosp-A", "doc-a", rxBody); rec.Code != http.StatusCreated {
		t.Fatalf("rx: %d", rec.Code)
	}
	if rec := doRequest(t, srv, http.MethodPost, "/v1/patients/pat-a/diagnoses", "hosp-A", "doc-a", map[string]string{"disease": "Migraine"}); rec.Code != http.StatusCreated {
		t.Fatalf("diagnose: %d", rec.Code)
	}

	// Doctor views the overview (defaults to now).
	rec := doRequest(t, srv, http.MethodGet, "/v1/patients/pat-a/overview", "hosp-A", "doc-a", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("doctor overview: %d %s", rec.Code, rec.Body.String())
	}
	var ov clinical.PatientOverview
	_ = json.Unmarshal(rec.Body.Bytes(), &ov)
	if len(ov.Diagnoses) != 1 || len(ov.ActivePrescriptions) != 1 || len(ov.Appointments) != 1 {
		t.Errorf("overview = %d diagnoses, %d rx, %d appts; want 1/1/1", len(ov.Diagnoses), len(ov.ActivePrescriptions), len(ov.Appointments))
	}
	if len(ov.Appointments) == 1 && len(ov.Appointments[0].Notes) != 1 {
		t.Errorf("appointment notes = %d, want 1", len(ov.Appointments[0].Notes))
	}

	// Patient views their own.
	if rec := doRequest(t, srv, http.MethodGet, "/v1/patients/pat-a/overview", "hosp-A", "pat-a", nil); rec.Code != http.StatusOK {
		t.Errorf("self overview = %d, want 200", rec.Code)
	}
	// Another patient is forbidden.
	if rec := doRequest(t, srv, http.MethodGet, "/v1/patients/pat-a/overview", "hosp-A", "pat-a2", nil); rec.Code != http.StatusForbidden {
		t.Errorf("other patient = %d, want 403", rec.Code)
	}
	// A pharmacist is forbidden.
	if rec := doRequest(t, srv, http.MethodGet, "/v1/patients/pat-a/overview", "hosp-A", "pharm-a", nil); rec.Code != http.StatusForbidden {
		t.Errorf("pharmacist = %d, want 403", rec.Code)
	}
	// Unauthenticated.
	if rec := doRequest(t, srv, http.MethodGet, "/v1/patients/pat-a/overview", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth = %d, want 401", rec.Code)
	}
	// Bad 'at' timestamp is a validation error.
	if rec := doRequest(t, srv, http.MethodGet, "/v1/patients/pat-a/overview?at=not-a-time", "hosp-A", "doc-a", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad at = %d, want 400", rec.Code)
	}
}
