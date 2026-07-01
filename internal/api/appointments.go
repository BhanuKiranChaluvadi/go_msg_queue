package api

import (
	"net/http"

	"medconnect/internal/appointments"
)

// handleBookAppointment lets a patient book an open timeslot.
// POST /v1/appointments  {"doctorId": "...", "timeslotId": "..."}
func (s *Server) handleBookAppointment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DoctorID   string `json:"doctorId"`
		TimeslotID string `json:"timeslotId"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	appt, err := s.Appointments.Book(r.Context(),
		appointments.BookInput{DoctorID: body.DoctorID, TimeslotID: body.TimeslotID})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, appt)
}
