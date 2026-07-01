package appointments

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
	"medconnect/internal/store/memory"
	"medconnect/internal/tenancy"
)

func dispatchFixtures() (*Service, *memory.PrescriptionStore, *capturePublisher) {
	rxStore := memory.NewPrescriptionStore()
	pub := &capturePublisher{}
	svc := NewService(Deps{
		Timeslots:     memory.NewTimeslotStore(),
		Appointments:  memory.NewAppointmentStore(),
		Notes:         memory.NewNoteStore(),
		Prescriptions: rxStore,
		Clock:         platform.NewFakeClock(testNow),
		IDGen:         platform.NewFakeIDGen("rx-"),
		Events:        pub,
	})
	return svc, rxStore, pub
}

func pharmacistCtxD(tenant, id string) context.Context {
	return tenancy.WithActor(context.Background(),
		tenancy.Actor{ID: id, TenantID: tenant, Role: domain.RolePharmacist})
}

func seedRx(t *testing.T, store *memory.PrescriptionStore, id, tenant string, status domain.PrescriptionStatus, expires time.Time) {
	t.Helper()
	if err := store.Create(context.Background(), domain.Prescription{
		ID: id, TenantID: tenant, AppointmentID: "appt-1", PatientID: "pat-1",
		Medication: "Aspirin", IssuedAt: testNow, ExpiresAt: expires, Status: status,
	}); err != nil {
		t.Fatalf("seed rx: %v", err)
	}
}

func TestListActivePrescriptions_FilterAndScope(t *testing.T) {
	svc, store, _ := dispatchFixtures()
	future := testNow.Add(24 * time.Hour)
	past := testNow.Add(-time.Hour)

	seedRx(t, store, "active-1", "hosp-A", domain.PrescriptionActive, future)
	seedRx(t, store, "dispatched", "hosp-A", domain.PrescriptionDispatched, future)
	seedRx(t, store, "expired", "hosp-A", domain.PrescriptionActive, past) // active status but past expiry
	seedRx(t, store, "other-tenant", "hosp-B", domain.PrescriptionActive, future)

	got, err := svc.ListActivePrescriptions(pharmacistCtxD("hosp-A", "pharm-1"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].ID != "active-1" {
		t.Errorf("active = %v, want [active-1] (dispatched, expired, other-tenant excluded)", rxIDs(got))
	}

	// Tenant B sees only its own active prescription.
	b, _ := svc.ListActivePrescriptions(pharmacistCtxD("hosp-B", "pharm-2"))
	if len(b) != 1 || b[0].ID != "other-tenant" {
		t.Errorf("hosp-B active = %v, want [other-tenant]", rxIDs(b))
	}
}

func TestListActivePrescriptions_NoTenantForbidden(t *testing.T) {
	svc, _, _ := dispatchFixtures()
	if _, err := svc.ListActivePrescriptions(context.Background()); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

func rxIDs(rx []domain.Prescription) []string {
	out := make([]string, len(rx))
	for i, p := range rx {
		out[i] = p.ID
	}
	return out
}

func TestDispatchPrescription_Success(t *testing.T) {
	svc, store, pub := dispatchFixtures()
	seedRx(t, store, "rx-1", "hosp-A", domain.PrescriptionActive, testNow.Add(24*time.Hour))

	rx, err := svc.DispatchPrescription(pharmacistCtxD("hosp-A", "pharm-1"), "rx-1")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if rx.Status != domain.PrescriptionDispatched {
		t.Errorf("status = %q, want dispatched", rx.Status)
	}
	// Persisted as dispatched.
	stored, _ := store.Get(context.Background(), "hosp-A", "rx-1")
	if stored.Status != domain.PrescriptionDispatched {
		t.Errorf("stored status = %q, want dispatched", stored.Status)
	}
	// Event emitted with pharmacist identity.
	evs := pub.byType(domain.EventPrescriptionDispatched)
	if len(evs) != 1 || evs[0].Payload["pharmacistId"] != "pharm-1" {
		t.Errorf("events = %+v, want one dispatched by pharm-1", evs)
	}
}

func TestDispatchPrescription_AlreadyDispatchedOrExpired(t *testing.T) {
	svc, store, _ := dispatchFixtures()
	seedRx(t, store, "done", "hosp-A", domain.PrescriptionDispatched, testNow.Add(24*time.Hour))
	seedRx(t, store, "old", "hosp-A", domain.PrescriptionActive, testNow.Add(-time.Hour)) // expired

	if _, err := svc.DispatchPrescription(pharmacistCtxD("hosp-A", "pharm-1"), "done"); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("dispatched err = %v, want ErrConflict", err)
	}
	if _, err := svc.DispatchPrescription(pharmacistCtxD("hosp-A", "pharm-1"), "old"); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expired err = %v, want ErrConflict", err)
	}
}

func TestDispatchPrescription_UnknownCrossTenantNoActor(t *testing.T) {
	svc, store, _ := dispatchFixtures()
	seedRx(t, store, "rx-1", "hosp-A", domain.PrescriptionActive, testNow.Add(24*time.Hour))

	if _, err := svc.DispatchPrescription(pharmacistCtxD("hosp-A", "pharm-1"), "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown err = %v, want ErrNotFound", err)
	}
	if _, err := svc.DispatchPrescription(pharmacistCtxD("hosp-B", "pharm-2"), "rx-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant err = %v, want ErrNotFound", err)
	}
	if _, err := svc.DispatchPrescription(context.Background(), "rx-1"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("no-actor err = %v, want ErrForbidden", err)
	}
}

// TestDispatchPrescription_ConcurrentExactlyOnce proves that many concurrent
// dispatches of the same active prescription result in exactly one success.
func TestDispatchPrescription_ConcurrentExactlyOnce(t *testing.T) {
	svc, store, pub := dispatchFixtures()
	seedRx(t, store, "rx-1", "hosp-A", domain.PrescriptionActive, testNow.Add(24*time.Hour))

	const n = 25
	var success, conflict int64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.DispatchPrescription(pharmacistCtxD("hosp-A", "pharm-1"), "rx-1")
			switch {
			case err == nil:
				atomic.AddInt64(&success, 1)
			case errors.Is(err, domain.ErrConflict):
				atomic.AddInt64(&conflict, 1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if success != 1 || conflict != n-1 {
		t.Errorf("success=%d conflict=%d, want 1 and %d", success, conflict, n-1)
	}
	if got := len(pub.byType(domain.EventPrescriptionDispatched)); got != 1 {
		t.Errorf("dispatched events = %d, want 1", got)
	}
}
