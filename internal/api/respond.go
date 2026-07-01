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

// listResponse is the envelope for collection endpoints. Wrapping the items in
// an object (rather than returning a bare array) lets the response grow — for
// example to add pagination metadata — without breaking existing clients.
type listResponse[T any] struct {
	Data []T `json:"data"`
}

// writeList writes items as a listResponse. A nil slice is emitted as an empty
// array so clients always see "data": [].
func writeList[T any](w http.ResponseWriter, status int, items []T) {
	if items == nil {
		items = []T{}
	}
	writeJSON(w, status, listResponse[T]{Data: items})
}

// maxRequestBody caps the size of a JSON request body. It bounds memory per
// request and rejects oversized or runaway payloads at the boundary.
const maxRequestBody = 1 << 20 // 1 MiB

// decodeJSON decodes a single JSON value from the request body into dst,
// rejecting unknown fields, bodies larger than maxRequestBody, and any trailing
// data after the value. A malformed body is reported as a validation error.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: unexpected trailing data after JSON body", domain.ErrValidation)
	}
	return nil
}
