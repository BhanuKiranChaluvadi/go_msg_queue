package appointments

import (
	"context"
	"errors"
	"testing"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
	"medconnect/internal/store/memory"
)

func rxFixtures() (*Service, *memory.AppointmentStore, *memory.PrescriptionStore, *capturePublisher) {
	apptStore := memory.NewAppointmentStore()
	rxStore := memory.NewPrescriptionStore()
	pub := &capturePublisher{}
	svc := NewService(Deps{
		Timeslots:     memory.NewTimeslotStore(),
		Appointments:  apptStore,
		Notes:         memory.NewNoteStore(),
		Prescriptions: rxStore,
		Clock:         platform.NewFakeClock(testNow),
		IDGen:         platform.NewFakeIDGen("rx-"),
		Events:        pub,
	})
	return svc, apptStore, rxStore, pub
}

func TestIssuePrescription_Success(t *testing.T) {
	svc, apptStore, rxStore, pub := rxFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1"})

	expires := testNow.Add(30 * 24 * time.Hour)
	rx, err := svc.IssuePrescription(doctorCtx("hosp-A", "doc-1"),
		IssuePrescriptionInput{AppointmentID: "appt-1", Medication: "Aspirin 100mg", ExpiresAt: expires})
	if err != nil {
		t.Fatalf("IssuePrescription: %v", err)
	}
	if rx.Status != domain.PrescriptionActive || rx.PatientID != "pat-1" || rx.Medication != "Aspirin 100mg" {
		t.Errorf("rx = %+v, want active/pat-1/Aspirin", rx)
	}
	if !rx.IsActive(testNow) {
		t.Error("new prescription should be active now")
	}

	// Persisted under the appointment.
	stored, _ := rxStore.ListByAppointment(context.Background(), "hosp-A", "appt-1")
	if len(stored) != 1 {
		t.Errorf("stored = %d, want 1", len(stored))
	}

	// prescription_added event carries the expected data.
	evs := pub.byType(domain.EventPrescriptionAdded)
	if len(evs) != 1 {
		t.Fatalf("prescription_added events = %d, want 1", len(evs))
	}
	d := evs[0].Payload
	if d["prescriptionId"] != rx.ID || d["medication"] != "Aspirin 100mg" || d["patientId"] != "pat-1" {
		t.Errorf("event payload = %+v, missing expected fields", d)
	}
	if got, _ := d["expiresAt"].(time.Time); !got.Equal(expires) {
		t.Errorf("event expiresAt = %v, want %v", d["expiresAt"], expires)
	}
}

func TestIssuePrescription_Validation(t *testing.T) {
	svc, apptStore, _, _ := rxFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1"})

	tests := []struct {
		name       string
		medication string
		expires    time.Time
	}{
		{"empty medication", "  ", testNow.Add(time.Hour)},
		{"expiry in the past", "Aspirin", testNow.Add(-time.Hour)},
		{"expiry now (not after)", "Aspirin", testNow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.IssuePrescription(doctorCtx("hosp-A", "doc-1"),
				IssuePrescriptionInput{AppointmentID: "appt-1", Medication: tt.medication, ExpiresAt: tt.expires})
			if !errors.Is(err, domain.ErrValidation) {
				t.Errorf("err = %v, want ErrValidation", err)
			}
		})
	}
}

func TestIssuePrescription_UnknownAppointment(t *testing.T) {
	svc, _, _, _ := rxFixtures()
	_, err := svc.IssuePrescription(doctorCtx("hosp-A", "doc-1"),
		IssuePrescriptionInput{AppointmentID: "nope", Medication: "Aspirin", ExpiresAt: testNow.Add(time.Hour)})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestIssuePrescription_NotOwningDoctorForbidden(t *testing.T) {
	svc, apptStore, _, _ := rxFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1"})
	_, err := svc.IssuePrescription(doctorCtx("hosp-A", "doc-2"),
		IssuePrescriptionInput{AppointmentID: "appt-1", Medication: "Aspirin", ExpiresAt: testNow.Add(time.Hour)})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

func TestIssuePrescription_MultiTenantIsolation(t *testing.T) {
	svc, apptStore, rxStore, _ := rxFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1"})

	// Same doctor id in another hospital cannot see the appointment.
	_, err := svc.IssuePrescription(doctorCtx("hosp-B", "doc-1"),
		IssuePrescriptionInput{AppointmentID: "appt-1", Medication: "Aspirin", ExpiresAt: testNow.Add(time.Hour)})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant err = %v, want ErrNotFound", err)
	}
	if got, _ := rxStore.ListByTenant(context.Background(), "hosp-B"); len(got) != 0 {
		t.Errorf("hosp-B prescriptions = %d, want 0", len(got))
	}
}

func TestIssuePrescription_NoActorForbidden(t *testing.T) {
	svc, _, _, _ := rxFixtures()
	_, err := svc.IssuePrescription(context.Background(),
		IssuePrescriptionInput{AppointmentID: "appt-1", Medication: "Aspirin", ExpiresAt: testNow.Add(time.Hour)})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}
