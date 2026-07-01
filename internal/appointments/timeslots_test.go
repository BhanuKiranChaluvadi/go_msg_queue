package appointments

import (
	"context"
	"errors"
	"testing"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
	"medconnect/internal/store/memory"
	"medconnect/internal/tenancy"
)

// testNow is the fixed "current time" used by the fake clock in these tests.
var testNow = time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)

// fixtures builds a Service over a fresh in-memory timeslot store, plus that
// store so tests can seed state directly. Ids are deterministic ("ts-1", ...).
func fixtures() (*Service, *memory.TimeslotStore) {
	ts := memory.NewTimeslotStore()
	svc := NewService(ts, platform.NewFakeClock(testNow), platform.NewFakeIDGen("ts-"))
	return svc, ts
}

func doctorCtx(tenant, doctorID string) context.Context {
	return tenancy.WithActor(context.Background(),
		tenancy.Actor{ID: doctorID, TenantID: tenant, Role: domain.RoleDoctor})
}

func patientCtx(tenant, patientID string) context.Context {
	return tenancy.WithActor(context.Background(),
		tenancy.Actor{ID: patientID, TenantID: tenant, Role: domain.RolePatient})
}

// hour returns testNow + n hours, a convenient future time for slots.
func hour(n int) time.Time { return testNow.Add(time.Duration(n) * time.Hour) }

func TestRegisterTimeslot_Success(t *testing.T) {
	svc, _ := fixtures()
	ts, err := svc.RegisterTimeslot(doctorCtx("hosp-A", "doc-1"),
		RegisterTimeslotInput{Start: hour(1), End: hour(2)})
	if err != nil {
		t.Fatalf("RegisterTimeslot: %v", err)
	}
	if ts.ID != "ts-1" || ts.DoctorID != "doc-1" || ts.TenantID != "hosp-A" {
		t.Errorf("timeslot = %+v, want id ts-1 / doc-1 / hosp-A", ts)
	}
	if ts.Status != domain.TimeslotOpen {
		t.Errorf("status = %q, want open", ts.Status)
	}
}

func TestRegisterTimeslot_Validation(t *testing.T) {
	svc, _ := fixtures()
	tests := []struct {
		name  string
		start time.Time
		end   time.Time
	}{
		{"end before start", hour(2), hour(1)},
		{"zero-length slot", hour(1), hour(1)},
		{"start in the past", hour(-2), hour(-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.RegisterTimeslot(doctorCtx("hosp-A", "doc-1"),
				RegisterTimeslotInput{Start: tt.start, End: tt.end})
			if !errors.Is(err, domain.ErrValidation) {
				t.Errorf("err = %v, want ErrValidation", err)
			}
		})
	}
}

func TestRegisterTimeslot_NoActorForbidden(t *testing.T) {
	svc, _ := fixtures()
	_, err := svc.RegisterTimeslot(context.Background(),
		RegisterTimeslotInput{Start: hour(1), End: hour(2)})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

func TestRegisterTimeslot_OverlapSameDoctor(t *testing.T) {
	svc, _ := fixtures()
	ctx := doctorCtx("hosp-A", "doc-1")
	if _, err := svc.RegisterTimeslot(ctx, RegisterTimeslotInput{Start: hour(1), End: hour(3)}); err != nil {
		t.Fatalf("first: %v", err)
	}

	// Overlapping the existing [1,3) slot must conflict.
	overlapping := []struct {
		name       string
		start, end time.Time
	}{
		{"contained", hour(1), hour(2)},
		{"straddles start", hour(0), hour(2)},
		{"straddles end", hour(2), hour(4)},
		{"identical", hour(1), hour(3)},
	}
	for _, tt := range overlapping {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.RegisterTimeslot(ctx, RegisterTimeslotInput{Start: tt.start, End: tt.end})
			if !errors.Is(err, domain.ErrConflict) {
				t.Errorf("err = %v, want ErrConflict", err)
			}
		})
	}

	// Adjacent slots (touching but not overlapping) are allowed.
	if _, err := svc.RegisterTimeslot(ctx, RegisterTimeslotInput{Start: hour(3), End: hour(4)}); err != nil {
		t.Errorf("adjacent slot should be allowed, got %v", err)
	}
}

func TestRegisterTimeslot_MultiDoctorIndependent(t *testing.T) {
	svc, _ := fixtures()
	// Two doctors in the SAME hospital may hold overlapping slots — availability
	// is per-doctor, not per-hospital.
	if _, err := svc.RegisterTimeslot(doctorCtx("hosp-A", "doc-1"),
		RegisterTimeslotInput{Start: hour(1), End: hour(2)}); err != nil {
		t.Fatalf("doc-1: %v", err)
	}
	if _, err := svc.RegisterTimeslot(doctorCtx("hosp-A", "doc-2"),
		RegisterTimeslotInput{Start: hour(1), End: hour(2)}); err != nil {
		t.Errorf("doc-2 overlapping doc-1 should be allowed, got %v", err)
	}
}

func TestRegisterTimeslot_MultiTenantIsolation(t *testing.T) {
	svc, _ := fixtures()
	// The same doctor id existing in two hospitals is two distinct doctors; an
	// overlapping slot in hospital B must not conflict with hospital A.
	if _, err := svc.RegisterTimeslot(doctorCtx("hosp-A", "doc-1"),
		RegisterTimeslotInput{Start: hour(1), End: hour(2)}); err != nil {
		t.Fatalf("hosp-A: %v", err)
	}
	if _, err := svc.RegisterTimeslot(doctorCtx("hosp-B", "doc-1"),
		RegisterTimeslotInput{Start: hour(1), End: hour(2)}); err != nil {
		t.Errorf("hosp-B same time should be allowed, got %v", err)
	}

	// Each hospital only sees its own slot.
	a, _ := svc.ListOpenTimeslots(patientCtx("hosp-A", "pat-x"), "doc-1")
	b, _ := svc.ListOpenTimeslots(patientCtx("hosp-B", "pat-y"), "doc-1")
	if len(a) != 1 || len(b) != 1 {
		t.Errorf("hosp-A saw %d, hosp-B saw %d; want 1 each", len(a), len(b))
	}
}

func TestListOpenTimeslots_FiltersAndOrders(t *testing.T) {
	svc, store := fixtures()
	ctx := doctorCtx("hosp-A", "doc-1")

	// Register three future slots out of chronological order.
	_, _ = svc.RegisterTimeslot(ctx, RegisterTimeslotInput{Start: hour(5), End: hour(6)}) // ts-1
	_, _ = svc.RegisterTimeslot(ctx, RegisterTimeslotInput{Start: hour(1), End: hour(2)}) // ts-2
	_, _ = svc.RegisterTimeslot(ctx, RegisterTimeslotInput{Start: hour(3), End: hour(4)}) // ts-3

	// Seed a booked slot and a past slot directly in the store; both must be excluded.
	_ = store.Create(ctx, domain.Timeslot{ID: "booked", TenantID: "hosp-A", DoctorID: "doc-1", Start: hour(7), End: hour(8), Status: domain.TimeslotBooked})
	_ = store.Create(ctx, domain.Timeslot{ID: "past", TenantID: "hosp-A", DoctorID: "doc-1", Start: hour(-3), End: hour(-2), Status: domain.TimeslotOpen})
	// A slot for a different doctor must not appear.
	_ = store.Create(ctx, domain.Timeslot{ID: "other-doc", TenantID: "hosp-A", DoctorID: "doc-2", Start: hour(1), End: hour(2), Status: domain.TimeslotOpen})

	got, err := svc.ListOpenTimeslots(patientCtx("hosp-A", "pat-1"), "doc-1")
	if err != nil {
		t.Fatalf("ListOpenTimeslots: %v", err)
	}
	wantIDs := []string{"ts-2", "ts-3", "ts-1"} // ordered by start: 1,3,5
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d slots %v, want %d", len(got), idsOf(got), len(wantIDs))
	}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Errorf("slot[%d] = %q, want %q (order/filter wrong): %v", i, got[i].ID, id, idsOf(got))
		}
	}
}

func TestListOpenTimeslots_NoTenantForbidden(t *testing.T) {
	svc, _ := fixtures()
	if _, err := svc.ListOpenTimeslots(context.Background(), "doc-1"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

func idsOf(ts []domain.Timeslot) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}
