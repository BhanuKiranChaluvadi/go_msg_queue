package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
)

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]string{"hello": "world"})

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), `"hello":"world"`) {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestWriteErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not found", fmt.Errorf("timeslot: %w", domain.ErrNotFound), http.StatusNotFound, "not_found"},
		{"conflict", fmt.Errorf("booking: %w", domain.ErrConflict), http.StatusConflict, "conflict"},
		{"validation", fmt.Errorf("bad: %w", domain.ErrValidation), http.StatusBadRequest, "validation"},
		{"forbidden", fmt.Errorf("no: %w", domain.ErrForbidden), http.StatusForbidden, "forbidden"},
		{"internal", fmt.Errorf("boom"), http.StatusInternalServerError, "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeError(rec, tt.err)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Error.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", body.Error.Code, tt.wantCode)
			}
			// Internal errors must not leak their underlying message.
			if tt.wantStatus == http.StatusInternalServerError && body.Error.Message != "internal error" {
				t.Errorf("internal message leaked: %q", body.Error.Message)
			}
		})
	}
}

func TestRequestIDGeneratedAndPropagated(t *testing.T) {
	gen := platform.NewFakeIDGen("req-")
	var seen string
	h := RequestID(gen)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	// Generated when absent.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if seen != "req-1" || rec.Header().Get("X-Request-ID") != "req-1" {
		t.Errorf("generated id = %q, header = %q, want req-1", seen, rec.Header().Get("X-Request-ID"))
	}

	// Honoured when supplied.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "abc")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if seen != "abc" || rec.Header().Get("X-Request-ID") != "abc" {
		t.Errorf("propagated id = %q, header = %q, want abc", seen, rec.Header().Get("X-Request-ID"))
	}
}

func TestLoggingPreservesStatusAndLogs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/brew", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("downstream status = %d, want 418", rec.Code)
	}
	logLine := buf.String()
	for _, want := range []string{`"status":418`, `"path":"/brew"`, `"method":"GET"`} {
		if !strings.Contains(logLine, want) {
			t.Errorf("log %q missing %q", logLine, want)
		}
	}
}

func TestChainOrder(t *testing.T) {
	var order []string
	mw := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	final := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { order = append(order, "handler") })
	h := Chain(final, mw("first"), mw("second"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"first", "second", "handler"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}
