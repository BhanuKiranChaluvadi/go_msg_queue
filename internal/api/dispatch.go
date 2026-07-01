package api

import "net/http"

// handleListActivePrescriptions returns the tenant's active prescriptions for a
// pharmacist to dispatch. GET /v1/prescriptions?status=active
func (s *Server) handleListActivePrescriptions(w http.ResponseWriter, r *http.Request) {
	if status := r.URL.Query().Get("status"); status != "" && status != "active" {
		// Only the active worklist is supported for now.
		writeJSON(w, http.StatusOK, []struct{}{})
		return
	}
	list, err := s.Appointments.ListActivePrescriptions(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
