package api

import (
	"net/http"
	"time"

	"medconnect/internal/appointments"
)

// handleIssuePrescription lets the appointment's doctor issue a follow-up prescription.
// POST /v1/appointments/{id}/prescriptions  {"medication": "...", "expiresAt": "..."}
func (s *Server) handleIssuePrescription(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Medication string    `json:"medication"`
		ExpiresAt  time.Time `json:"expiresAt"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	rx, err := s.Appointments.IssuePrescription(r.Context(), appointments.IssuePrescriptionInput{
		AppointmentID: r.PathValue("id"),
		Medication:    body.Medication,
		ExpiresAt:     body.ExpiresAt,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rx)
}
