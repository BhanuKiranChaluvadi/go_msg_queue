package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"medconnect/internal/domain"
)

// bookAppointment registers a slot for `doctor` and books it as `patient`,
// returning the created appointment id.
func bookAppointment(t *testing.T, srv *Server, tenant, doctor, patient string, hFrom, hTo int) string {
	t.Helper()
	slot := registerSlot(t, srv, tenant, doctor, hFrom, hTo)
	rec := doRequest(t, srv, http.MethodPost, "/v1/appointments", tenant, patient, bookReq{doctor, slot})
	if rec.Code != http.StatusCreated {
		t.Fatalf("bookAppointment: status %d, body %s", rec.Code, rec.Body.String())
	}
	var appt domain.Appointment
	if err := json.Unmarshal(rec.Body.Bytes(), &appt); err != nil {
		t.Fatalf("bookAppointment decode: %v", err)
	}
	return appt.ID
}

func TestAddNote_Handler(t *testing.T) {
	srv := newTestServer()
	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2)

	type noteReq struct {
		Text string `json:"text"`
	}
	notePath := "/v1/appointments/" + apptID + "/notes"

	// Owning doctor adds a note.
	rec := doRequest(t, srv, http.MethodPost, notePath, "hosp-A", "doc-a", noteReq{"Patient reports headache."})
	if rec.Code != http.StatusCreated {
		t.Fatalf("doctor add note: %d %s", rec.Code, rec.Body.String())
	}
	var note domain.Note
	_ = json.Unmarshal(rec.Body.Bytes(), &note)
	if note.Source != domain.NoteSourceManual || note.Status != domain.NoteComplete {
		t.Errorf("note = %+v, want manual/complete", note)
	}

	// Patient may not add notes.
	if rec := doRequest(t, srv, http.MethodPost, notePath, "hosp-A", "pat-a", noteReq{"hi"}); rec.Code != http.StatusForbidden {
		t.Errorf("patient add note = %d, want 403", rec.Code)
	}
	// Unauthenticated.
	if rec := doRequest(t, srv, http.MethodPost, notePath, "", "", noteReq{"hi"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth add note = %d, want 401", rec.Code)
	}
	// Unknown appointment.
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/nope/notes", "hosp-A", "doc-a", noteReq{"hi"}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown appt add note = %d, want 404", rec.Code)
	}
	// A doctor from another hospital cannot annotate this appointment.
	if rec := doRequest(t, srv, http.MethodPost, notePath, "hosp-B", "doc-b", noteReq{"hi"}); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant add note = %d, want 404", rec.Code)
	}
}
