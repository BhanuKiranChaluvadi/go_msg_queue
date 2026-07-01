package api

import (
	"net/http"
	"time"

	"medconnect/internal/appointments"
)

// handleRegisterTimeslot lets a doctor publish an availability slot.
// POST /v1/timeslots  {"start": "...", "end": "..."}
func (s *Server) handleRegisterTimeslot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	ts, err := s.Appointments.RegisterTimeslot(r.Context(),
		appointments.RegisterTimeslotInput{Start: body.Start, End: body.End})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ts)
}

// handleListDoctorTimeslots returns a doctor's open, upcoming slots.
// GET /v1/doctors/{doctorId}/timeslots
func (s *Server) handleListDoctorTimeslots(w http.ResponseWriter, r *http.Request) {
	doctorID := r.PathValue("doctorId")
	list, err := s.Appointments.ListOpenTimeslots(r.Context(), doctorID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeList(w, http.StatusOK, list)
}
