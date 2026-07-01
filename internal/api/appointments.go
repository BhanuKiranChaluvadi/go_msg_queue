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
	if err := decodeJSON(w, r, &body); err != nil {
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

// handleNextAppointments lists the calling doctor's upcoming appointments.
// GET /v1/appointments/next
func (s *Server) handleNextAppointments(w http.ResponseWriter, r *http.Request) {
	list, err := s.Appointments.NextAppointments(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleGetAppointment returns an appointment with its notes and prescriptions.
// Visible to the appointment's doctor and its patient only.
// GET /v1/appointments/{id}
func (s *Server) handleGetAppointment(w http.ResponseWriter, r *http.Request) {
	ov, err := s.Appointments.AppointmentOverview(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ov)
}
