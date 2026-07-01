package api

import "net/http"

// handleAnalytics returns the caller's tenant usage summary.
// GET /v1/analytics
func (s *Server) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	sum, err := s.Analytics.Summarize(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}
