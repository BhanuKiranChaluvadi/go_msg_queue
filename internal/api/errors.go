package api

import (
	"errors"
	"net/http"

	"medconnect/internal/domain"
)

// errorBody is the JSON error envelope returned for every failed request:
// {"error": {"code": "...", "message": "..."}}.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// statusFor maps a domain sentinel error to an HTTP status and stable error code.
func statusFor(err error) (int, string) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest, "validation"
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, "forbidden"
	default:
		return http.StatusInternalServerError, "internal"
	}
}

// writeError maps err to a status and JSON envelope. Internal (500) errors never
// leak their message to the client; callers should log the underlying error.
func writeError(w http.ResponseWriter, err error) {
	status, code := statusFor(err)
	msg := err.Error()
	if status == http.StatusInternalServerError {
		msg = "internal error"
	}
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: msg}})
}
