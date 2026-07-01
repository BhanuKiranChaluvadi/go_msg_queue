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
