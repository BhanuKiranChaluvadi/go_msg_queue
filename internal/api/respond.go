package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"medconnect/internal/domain"
)

// writeJSON encodes v as JSON with the given status code. A nil v writes only
// the status (useful for 204/202 with no body).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// decodeJSON decodes the request body into dst, rejecting unknown fields. A
// malformed body is reported as a validation error.
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	return nil
}
