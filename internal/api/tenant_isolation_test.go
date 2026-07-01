package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"medconnect/internal/analytics"
	"medconnect/internal/clinical"
	"medconnect/internal/domain"
)

// TestTenantIsolation proves that data created in hospital A is never visible to
// hospital B across every read/mutation endpoint.
func TestTenantIsolation(t *testing.T) {
	srv := newTestServer()

	// --- Seed a full record in hospital A ---
	slot := registerSlot(t, srv, "hosp-A", "doc-a", 1, 2)
	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 3, 4)
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/notes", "hosp-A", "doc-a", map[string]string{"text": "private"}); rec.Code != http.StatusCreated {
		t.Fatalf("note: %d", rec.Code)
	}
	rxBody := map[string]any{"medication": "Aspirin", "expiresAt": apiTestNow.Add(30 * 24 * time.Hour)}
	rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/prescriptions", "hosp-A", "doc-a", rxBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("rx: %d", rec.Code)
	}
	var rx domain.Prescription
	_ = json.Unmarshal(rec.Body.Bytes(), &rx)
	rec = doRequest(t, srv, http.MethodPost, "/v1/patients/pat-a/diagnoses", "hosp-A", "doc-a", map[string]string{"disease": "Flu"})
	var diag domain.Diagnosis
	_ = json.Unmarshal(rec.Body.Bytes(), &diag)

	// --- Hospital B must not see or touch any of it ---

	// Appointment overview: not found for a B doctor.
	if rec := doRequest(t, srv, http.MethodGet, "/v1/appointments/"+apptID, "hosp-B", "doc-b", nil); rec.Code != http.StatusNotFound {
		t.Errorf("B appointment overview = %d, want 404", rec.Code)
	}
	// Doctor A's open slots are invisible in B.
	if rec := doRequest(t, srv, http.MethodGet, "/v1/doctors/doc-a/timeslots", "hosp-B", "pat-b", nil); rec.Code == http.StatusOK {
		var slots []domain.Timeslot
		_ = json.Unmarshal(rec.Body.Bytes(), &slots)
		if len(slots) != 0 {
			t.Errorf("B sees doc-a slots = %d, want 0", len(slots))
		}
	}
	// A B patient cannot book A's slot.
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments", "hosp-B", "pat-b", bookReq{"doc-a", slot}); rec.Code != http.StatusNotFound {
		t.Errorf("B book of A slot = %d, want 404", rec.Code)
	}
	// A B doctor's "next" list excludes A's appointment.
	if rec := doRequest(t, srv, http.MethodGet, "/v1/appointments/next", "hosp-B", "doc-b", nil); rec.Code == http.StatusOK {
		var appts []domain.Appointment
		_ = json.Unmarshal(rec.Body.Bytes(), &appts)
		if len(appts) != 0 {
			t.Errorf("B next appts = %d, want 0", len(appts))
		}
	}
	// A B pharmacist... there is none seeded; but a B doctor cannot dispatch A's rx.
	if rec := doRequest(t, srv, http.MethodPost, "/v1/prescriptions/"+rx.ID+"/dispatch", "hosp-B", "doc-b", nil); rec.Code == http.StatusOK {
		t.Errorf("B dispatch of A rx unexpectedly succeeded")
	}
	// Patient overview in B is empty for pat-a.
	if rec := doRequest(t, srv, http.MethodGet, "/v1/patients/pat-a/overview", "hosp-B", "doc-b", nil); rec.Code == http.StatusOK {
		var ov clinical.PatientOverview
		_ = json.Unmarshal(rec.Body.Bytes(), &ov)
		if len(ov.Diagnoses) != 0 || len(ov.ActivePrescriptions) != 0 || len(ov.Appointments) != 0 {
			t.Errorf("B patient overview leaked A data: %+v", ov)
		}
	}
	// Dismissing A's diagnosis from B is not found.
	if rec := doRequest(t, srv, http.MethodDelete, "/v1/patients/pat-a/diagnoses/"+diag.ID, "hosp-B", "doc-b", nil); rec.Code != http.StatusNotFound {
		t.Errorf("B dismiss of A diagnosis = %d, want 404", rec.Code)
	}
	// Audit in B shows nothing about pat-a.
	rec = doRequest(t, srv, http.MethodGet, "/v1/audit?patientId=pat-a", "hosp-B", "doc-b", nil)
	var recs []json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &recs)
	if len(recs) != 0 {
		t.Errorf("B audit for pat-a = %d records, want 0", len(recs))
	}
	// Analytics in B is all zeros.
	rec = doRequest(t, srv, http.MethodGet, "/v1/analytics", "hosp-B", "doc-b", nil)
	var sum analytics.Summary
	_ = json.Unmarshal(rec.Body.Bytes(), &sum)
	if sum.TotalAppointments != 0 || sum.ActivePatients != 0 || sum.PrescriptionCounts.Issued != 0 {
		t.Errorf("B analytics leaked A usage: %+v", sum)
	}

	// And hospital A still sees its own record intact.
	if rec := doRequest(t, srv, http.MethodGet, "/v1/appointments/"+apptID, "hosp-A", "doc-a", nil); rec.Code != http.StatusOK {
		t.Errorf("A can no longer see its own appointment: %d", rec.Code)
	}
}
