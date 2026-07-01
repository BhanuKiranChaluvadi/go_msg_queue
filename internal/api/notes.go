package api

import (
	"net/http"

	"medconnect/internal/appointments"
)

// handleAddNote lets the appointment's doctor attach a manual note.
// POST /v1/appointments/{id}/notes  {"text": "..."}
func (s *Server) handleAddNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	note, err := s.Appointments.AddNote(r.Context(),
		appointments.AddNoteInput{AppointmentID: r.PathValue("id"), Text: body.Text})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, note)
}
