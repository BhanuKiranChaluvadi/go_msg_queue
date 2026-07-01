package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"medconnect/internal/domain"
)

// registerSlot has doctor `user` in `tenant` create a slot and returns its id.
func registerSlot(t *testing.T, srv *Server, tenant, user string, hFrom, hTo int) string {
	t.Helper()
	rec := doRequest(t, srv, http.MethodPost, "/v1/timeslots", tenant, user, futureSlot(hFrom, hTo))
	if rec.Code != http.StatusCreated {
		t.Fatalf("registerSlot: status %d, body %s", rec.Code, rec.Body.String())
	}
	var ts domain.Timeslot
	if err := json.Unmarshal(rec.Body.Bytes(), &ts); err != nil {
		t.Fatalf("registerSlot decode: %v", err)
	}
	return ts.ID
}

type bookReq struct {
	DoctorID   string `json:"doctorId"`
	TimeslotID string `json:"timeslotId"`
}

func TestBookAppointment_Handler_AuthAndRoles(t *testing.T) {
	srv := newTestServer()
	slot := registerSlot(t, srv, "hosp-A", "doc-a", 1, 2)

	tests := []struct {
		name       string
		tenant     string
		user       string
		body       any
		wantStatus int
	}{
		{"unauthenticated", "", "", bookReq{"doc-a", slot}, http.StatusUnauthorized},
		{"doctor forbidden", "hosp-A", "doc-a", bookReq{"doc-a", slot}, http.StatusForbidden},
		{"unknown slot", "hosp-A", "pat-a", bookReq{"doc-a", "nope"}, http.StatusNotFound},
		{"patient books", "hosp-A", "pat-a", bookReq{"doc-a", slot}, http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, srv, http.MethodPost, "/v1/appointments", tt.tenant, tt.user, tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus == http.StatusCreated {
				var appt domain.Appointment
				_ = json.Unmarshal(rec.Body.Bytes(), &appt)
				if appt.PatientID != "pat-a" || appt.DoctorID != "doc-a" || appt.Status != domain.AppointmentScheduled {
					t.Errorf("appt = %+v, want pat-a/doc-a/scheduled", appt)
				}
			}
		})
	}
}

func TestBookAppointment_Handler_DoubleBookConflict(t *testing.T) {
	srv := newTestServer()
	slot := registerSlot(t, srv, "hosp-A", "doc-a", 1, 2)

	// First patient takes the slot.
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments", "hosp-A", "pat-a", bookReq{"doc-a", slot}); rec.Code != http.StatusCreated {
		t.Fatalf("first book: %d %s", rec.Code, rec.Body.String())
	}
	// A different patient in the same hospital cannot take the same slot.
	rec := doRequest(t, srv, http.MethodPost, "/v1/appointments", "hosp-A", "pat-a2", bookReq{"doc-a", slot})
	if rec.Code != http.StatusConflict {
		t.Errorf("second book status = %d, want 409", rec.Code)
	}
}

func TestBookAppointment_Handler_MultiTenantIsolation(t *testing.T) {
	srv := newTestServer()
	// Slot created in hospital A.
	slot := registerSlot(t, srv, "hosp-A", "doc-a", 1, 2)

	// A patient in hospital B cannot book hospital A's slot (not found in B).
	rec := doRequest(t, srv, http.MethodPost, "/v1/appointments", "hosp-B", "pat-b", bookReq{"doc-a", slot})
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant book status = %d, want 404", rec.Code)
	}
}
