package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"medconnect/internal/domain"
)

func TestIssuePrescription_Handler(t *testing.T) {
	srv := newTestServer()
	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2)

	type rxReq struct {
		Medication string    `json:"medication"`
		ExpiresAt  time.Time `json:"expiresAt"`
	}
	path := "/v1/appointments/" + apptID + "/prescriptions"
	future := apiTestNow.Add(30 * 24 * time.Hour)

	// Owning doctor issues a prescription.
	rec := doRequest(t, srv, http.MethodPost, path, "hosp-A", "doc-a", rxReq{"Aspirin 100mg", future})
	if rec.Code != http.StatusCreated {
		t.Fatalf("doctor issue: %d %s", rec.Code, rec.Body.String())
	}
	var rx domain.Prescription
	_ = json.Unmarshal(rec.Body.Bytes(), &rx)
	if rx.Status != domain.PrescriptionActive || rx.Medication != "Aspirin 100mg" || rx.PatientID != "pat-a" {
		t.Errorf("rx = %+v, want active/Aspirin/pat-a", rx)
	}

	// Patient may not prescribe.
	if rec := doRequest(t, srv, http.MethodPost, path, "hosp-A", "pat-a", rxReq{"Aspirin", future}); rec.Code != http.StatusForbidden {
		t.Errorf("patient issue = %d, want 403", rec.Code)
	}
	// Unauthenticated.
	if rec := doRequest(t, srv, http.MethodPost, path, "", "", rxReq{"Aspirin", future}); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth issue = %d, want 401", rec.Code)
	}
	// Past expiry is a validation error.
	if rec := doRequest(t, srv, http.MethodPost, path, "hosp-A", "doc-a", rxReq{"Aspirin", apiTestNow.Add(-time.Hour)}); rec.Code != http.StatusBadRequest {
		t.Errorf("past-expiry issue = %d, want 400", rec.Code)
	}
	// Unknown appointment.
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/nope/prescriptions", "hosp-A", "doc-a", rxReq{"Aspirin", future}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown appt issue = %d, want 404", rec.Code)
	}
	// A doctor from another hospital cannot prescribe on this appointment.
	if rec := doRequest(t, srv, http.MethodPost, path, "hosp-B", "doc-b", rxReq{"Aspirin", future}); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant issue = %d, want 404", rec.Code)
	}
}
