package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"medconnect/internal/audit"
)

func TestAudit_Handler_TracksMutations(t *testing.T) {
	srv := newTestServer()

	// Generate a spread of patient-data mutations for pat-a.
	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2) // appointment_booked
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/notes", "hosp-A", "doc-a", map[string]string{"text": "n"}); rec.Code != http.StatusCreated {
		t.Fatalf("note: %d", rec.Code)
	} // note_added
	rxBody := map[string]any{"medication": "Aspirin", "expiresAt": apiTestNow.Add(30 * 24 * time.Hour)}
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/prescriptions", "hosp-A", "doc-a", rxBody); rec.Code != http.StatusCreated {
		t.Fatalf("rx: %d", rec.Code)
	} // prescription_added
	if rec := doRequest(t, srv, http.MethodPost, "/v1/patients/pat-a/diagnoses", "hosp-A", "doc-a", map[string]string{"disease": "Flu"}); rec.Code != http.StatusCreated {
		t.Fatalf("diagnose: %d", rec.Code)
	} // diagnosis_added

	// Doctor queries the audit trail for pat-a.
	rec := doRequest(t, srv, http.MethodGet, "/v1/audit?patientId=pat-a", "hosp-A", "doc-a", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit: %d %s", rec.Code, rec.Body.String())
	}
	var recs []audit.Record
	_ = json.Unmarshal(rec.Body.Bytes(), &recs)

	types := map[string]bool{}
	for _, r := range recs {
		types[r.Type] = true
		if r.ActorID == "" || r.Timestamp.IsZero() {
			t.Errorf("audit record missing actor/timestamp: %+v", r)
		}
	}
	for _, want := range []string{"appointment_booked", "note_added", "prescription_added", "diagnosis_added"} {
		if !types[want] {
			t.Errorf("audit trail missing %q; got types %v", want, types)
		}
	}

	// Type filter narrows to a single kind.
	rec = doRequest(t, srv, http.MethodGet, "/v1/audit?patientId=pat-a&type=note_added", "hosp-A", "doc-a", nil)
	var noteOnly []audit.Record
	_ = json.Unmarshal(rec.Body.Bytes(), &noteOnly)
	if len(noteOnly) != 1 || noteOnly[0].Type != "note_added" {
		t.Errorf("type filter = %+v, want one note_added", noteOnly)
	}
}

func TestAudit_Handler_RoleAndValidation(t *testing.T) {
	srv := newTestServer()
	if rec := doRequest(t, srv, http.MethodGet, "/v1/audit", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth = %d, want 401", rec.Code)
	}
	if rec := doRequest(t, srv, http.MethodGet, "/v1/audit", "hosp-A", "pat-a", nil); rec.Code != http.StatusForbidden {
		t.Errorf("patient = %d, want 403", rec.Code)
	}
	if rec := doRequest(t, srv, http.MethodGet, "/v1/audit?from=bad", "hosp-A", "doc-a", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad from = %d, want 400", rec.Code)
	}
	if rec := doRequest(t, srv, http.MethodGet, "/v1/audit?from=2026-02-01T00:00:00Z&to=2026-01-01T00:00:00Z", "hosp-A", "doc-a", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("from after to = %d, want 400", rec.Code)
	}
}
