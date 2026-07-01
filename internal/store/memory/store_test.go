package memory

import (
	"context"
	"errors"
	"sync"
	"testing"

	"medconnect/internal/domain"
)

func newTimeslotStore() *TimeslotStore { return NewTimeslotStore() }

func TestStoreCRUD(t *testing.T) {
	ctx := context.Background()
	st := newTimeslotStore()
	ts := domain.Timeslot{ID: "ts1", TenantID: "A", DoctorID: "d1", Status: domain.TimeslotOpen}

	if err := st.Create(ctx, ts); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := st.Get(ctx, "A", "ts1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DoctorID != "d1" {
		t.Errorf("DoctorID = %q, want d1", got.DoctorID)
	}

	// Update flips the status.
	ts.Status = domain.TimeslotBooked
	if err := st.Update(ctx, ts); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = st.Get(ctx, "A", "ts1")
	if got.Status != domain.TimeslotBooked {
		t.Errorf("Status = %q, want booked", got.Status)
	}

	// Missing get and update surface ErrNotFound.
	if _, err := st.Get(ctx, "A", "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get(missing) err = %v, want ErrNotFound", err)
	}
	if err := st.Update(ctx, domain.Timeslot{ID: "nope", TenantID: "A"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Update(missing) err = %v, want ErrNotFound", err)
	}

	list, _ := st.ListByDoctor(ctx, "A", "d1")
	if len(list) != 1 {
		t.Errorf("ListByDoctor len = %d, want 1", len(list))
	}
}

func TestTenantIsolation(t *testing.T) {
	ctx := context.Background()
	st := NewTimeslotStore()
	_ = st.Create(ctx, domain.Timeslot{ID: "ts1", TenantID: "A", DoctorID: "d1"})

	// Tenant B cannot see tenant A's data.
	if _, err := st.Get(ctx, "B", "ts1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant Get err = %v, want ErrNotFound", err)
	}
	if list, _ := st.ListByDoctor(ctx, "B", "d1"); len(list) != 0 {
		t.Errorf("cross-tenant list len = %d, want 0", len(list))
	}
	// And A still sees its own.
	if _, err := st.Get(ctx, "A", "ts1"); err != nil {
		t.Errorf("same-tenant Get err = %v, want nil", err)
	}
}

func TestAppointmentExistsForPatientDoctor(t *testing.T) {
	ctx := context.Background()
	st := NewAppointmentStore()
	_ = st.Create(ctx, domain.Appointment{ID: "a1", TenantID: "A", DoctorID: "d1", PatientID: "p1", Status: domain.AppointmentScheduled})

	exists, _ := st.ExistsForPatientDoctor(ctx, "A", "p1", "d1")
	if !exists {
		t.Error("want existing appointment for p1/d1")
	}
	// Different pair, and cross-tenant, do not match.
	if e, _ := st.ExistsForPatientDoctor(ctx, "A", "p1", "d2"); e {
		t.Error("did not expect match for p1/d2")
	}
	if e, _ := st.ExistsForPatientDoctor(ctx, "B", "p1", "d1"); e {
		t.Error("did not expect cross-tenant match")
	}

	// A cancelled appointment does not count against the one-per-pair rule.
	_ = st.Update(ctx, domain.Appointment{ID: "a1", TenantID: "A", DoctorID: "d1", PatientID: "p1", Status: domain.AppointmentCancelled})
	if e, _ := st.ExistsForPatientDoctor(ctx, "A", "p1", "d1"); e {
		t.Error("cancelled appointment should not count")
	}
}

func TestStoreConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	st := NewTimeslotStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := string(rune('a'+n%26)) + "-slot"
			_ = st.Create(ctx, domain.Timeslot{ID: id, TenantID: "A", DoctorID: "d1"})
			_, _ = st.Get(ctx, "A", id)
			_, _ = st.ListByDoctor(ctx, "A", "d1")
		}(i)
	}
	wg.Wait()
}
