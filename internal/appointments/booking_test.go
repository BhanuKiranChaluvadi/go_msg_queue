package appointments

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
	"medconnect/internal/store/memory"
)

// capturePublisher records published events for assertions and is concurrency-safe.
type capturePublisher struct {
	mu     sync.Mutex
	events []domain.Event
}

func (c *capturePublisher) Publish(_ context.Context, e domain.Event) domain.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return e
}

func (c *capturePublisher) byType(t domain.EventType) []domain.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []domain.Event
	for _, e := range c.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// bookingFixtures wires a Service with references to the underlying stores and
// the capture publisher so booking tests can inspect all three.
func bookingFixtures() (*Service, *memory.TimeslotStore, *memory.AppointmentStore, *capturePublisher) {
	tsStore := memory.NewTimeslotStore()
	apptStore := memory.NewAppointmentStore()
	pub := &capturePublisher{}
	svc := NewService(Deps{
		Timeslots:    tsStore,
		Appointments: apptStore,
		Notes:        memory.NewNoteStore(),
		Clock:        platform.NewFakeClock(testNow),
		IDGen:        platform.NewFakeIDGen("ap-"),
		Events:       pub,
	})
	return svc, tsStore, apptStore, pub
}

// seedSlot inserts an open slot owned by doctorID in the given tenant.
func seedSlot(t *testing.T, store *memory.TimeslotStore, tenant, id, doctorID string) {
	t.Helper()
	if err := store.Create(context.Background(), domain.Timeslot{
		ID: id, TenantID: tenant, DoctorID: doctorID, Start: hour(1), End: hour(2), Status: domain.TimeslotOpen,
	}); err != nil {
		t.Fatalf("seed slot: %v", err)
	}
}

func TestBook_Success(t *testing.T) {
	svc, tsStore, apptStore, pub := bookingFixtures()
	seedSlot(t, tsStore, "hosp-A", "slot-1", "doc-1")

	appt, err := svc.Book(patientCtx("hosp-A", "pat-1"), BookInput{DoctorID: "doc-1", TimeslotID: "slot-1"})
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if appt.PatientID != "pat-1" || appt.DoctorID != "doc-1" || appt.Status != domain.AppointmentScheduled {
		t.Errorf("appt = %+v, want pat-1/doc-1/scheduled", appt)
	}

	// The slot is now booked.
	slot, _ := tsStore.Get(context.Background(), "hosp-A", "slot-1")
	if slot.Status != domain.TimeslotBooked {
		t.Errorf("slot status = %q, want booked", slot.Status)
	}
	// The appointment is persisted.
	if _, err := apptStore.Get(context.Background(), "hosp-A", appt.ID); err != nil {
		t.Errorf("appointment not stored: %v", err)
	}
	// An appointment_booked event was emitted for this appointment.
	evs := pub.byType(domain.EventAppointmentBooked)
	if len(evs) != 1 || evs[0].EntityRef != appt.ID || evs[0].ActorID != "pat-1" {
		t.Errorf("events = %+v, want one appointment_booked for %s by pat-1", evs, appt.ID)
	}
}

func TestBook_UnknownSlot(t *testing.T) {
	svc, _, _, _ := bookingFixtures()
	_, err := svc.Book(patientCtx("hosp-A", "pat-1"), BookInput{DoctorID: "doc-1", TimeslotID: "nope"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestBook_DoctorMismatch(t *testing.T) {
	svc, tsStore, _, _ := bookingFixtures()
	seedSlot(t, tsStore, "hosp-A", "slot-1", "doc-1")
	_, err := svc.Book(patientCtx("hosp-A", "pat-1"), BookInput{DoctorID: "doc-2", TimeslotID: "slot-1"})
	if !errors.Is(err, domain.ErrValidation) {
		t.Errorf("err = %v, want ErrValidation", err)
	}
}

func TestBook_DoubleBookRejected(t *testing.T) {
	svc, tsStore, _, _ := bookingFixtures()
	seedSlot(t, tsStore, "hosp-A", "slot-1", "doc-1")

	if _, err := svc.Book(patientCtx("hosp-A", "pat-1"), BookInput{DoctorID: "doc-1", TimeslotID: "slot-1"}); err != nil {
		t.Fatalf("first book: %v", err)
	}
	// A different patient cannot take the same slot.
	_, err := svc.Book(patientCtx("hosp-A", "pat-2"), BookInput{DoctorID: "doc-1", TimeslotID: "slot-1"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("err = %v, want ErrConflict (slot taken)", err)
	}
}

func TestBook_OnePerPatientDoctor(t *testing.T) {
	svc, tsStore, _, _ := bookingFixtures()
	seedSlot(t, tsStore, "hosp-A", "slot-1", "doc-1")
	seedSlot(t, tsStore, "hosp-A", "slot-2", "doc-1")

	if _, err := svc.Book(patientCtx("hosp-A", "pat-1"), BookInput{DoctorID: "doc-1", TimeslotID: "slot-1"}); err != nil {
		t.Fatalf("first book: %v", err)
	}
	// Same patient, same doctor, different slot -> rejected.
	_, err := svc.Book(patientCtx("hosp-A", "pat-1"), BookInput{DoctorID: "doc-1", TimeslotID: "slot-2"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("err = %v, want ErrConflict (one per patient-doctor)", err)
	}
}

func TestBook_SamePatientDifferentDoctorsAllowed(t *testing.T) {
	svc, tsStore, _, _ := bookingFixtures()
	seedSlot(t, tsStore, "hosp-A", "slot-1", "doc-1")
	seedSlot(t, tsStore, "hosp-A", "slot-2", "doc-2")

	if _, err := svc.Book(patientCtx("hosp-A", "pat-1"), BookInput{DoctorID: "doc-1", TimeslotID: "slot-1"}); err != nil {
		t.Fatalf("doc-1: %v", err)
	}
	if _, err := svc.Book(patientCtx("hosp-A", "pat-1"), BookInput{DoctorID: "doc-2", TimeslotID: "slot-2"}); err != nil {
		t.Errorf("booking a second, different doctor should be allowed, got %v", err)
	}
}

func TestBook_MultiTenantIsolation(t *testing.T) {
	svc, tsStore, _, _ := bookingFixtures()
	// Identically-named slot/doctor in two hospitals; a patient in one tenant must
	// not be able to book the other tenant's slot.
	seedSlot(t, tsStore, "hosp-A", "slot-1", "doc-1")
	seedSlot(t, tsStore, "hosp-B", "slot-1", "doc-1")

	// Patient in A books A's slot.
	if _, err := svc.Book(patientCtx("hosp-A", "pat-1"), BookInput{DoctorID: "doc-1", TimeslotID: "slot-1"}); err != nil {
		t.Fatalf("A book: %v", err)
	}
	// B's identically-id'd slot is still open and bookable by a B patient.
	if _, err := svc.Book(patientCtx("hosp-B", "pat-2"), BookInput{DoctorID: "doc-1", TimeslotID: "slot-1"}); err != nil {
		t.Errorf("B book of B's slot should succeed, got %v", err)
	}
}

// TestBook_ConcurrentSameSlot proves exactly one of many concurrent bookings of
// the same slot wins, under the race detector.
func TestBook_ConcurrentSameSlot(t *testing.T) {
	svc, tsStore, _, pub := bookingFixtures()
	seedSlot(t, tsStore, "hosp-A", "slot-1", "doc-1")

	const n = 25
	var success, conflict int64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		patient := "pat-" + string(rune('A'+i))
		go func(p string) {
			defer wg.Done()
			_, err := svc.Book(patientCtx("hosp-A", p), BookInput{DoctorID: "doc-1", TimeslotID: "slot-1"})
			switch {
			case err == nil:
				atomic.AddInt64(&success, 1)
			case errors.Is(err, domain.ErrConflict):
				atomic.AddInt64(&conflict, 1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(patient)
	}
	wg.Wait()

	if success != 1 {
		t.Errorf("successes = %d, want exactly 1", success)
	}
	if conflict != n-1 {
		t.Errorf("conflicts = %d, want %d", conflict, n-1)
	}
	if got := len(pub.byType(domain.EventAppointmentBooked)); got != 1 {
		t.Errorf("appointment_booked events = %d, want 1", got)
	}
}

// TestBook_ConcurrentSamePatientDoctor proves the one-per-pair invariant holds
// under concurrency: a patient racing to book two slots of the same doctor wins once.
func TestBook_ConcurrentSamePatientDoctor(t *testing.T) {
	svc, tsStore, _, _ := bookingFixtures()
	seedSlot(t, tsStore, "hosp-A", "slot-1", "doc-1")
	seedSlot(t, tsStore, "hosp-A", "slot-2", "doc-1")

	var success, conflict int64
	var wg sync.WaitGroup
	for _, slot := range []string{"slot-1", "slot-2"} {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			_, err := svc.Book(patientCtx("hosp-A", "pat-1"), BookInput{DoctorID: "doc-1", TimeslotID: s})
			switch {
			case err == nil:
				atomic.AddInt64(&success, 1)
			case errors.Is(err, domain.ErrConflict):
				atomic.AddInt64(&conflict, 1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(slot)
	}
	wg.Wait()

	if success != 1 || conflict != 1 {
		t.Errorf("success=%d conflict=%d, want 1 and 1", success, conflict)
	}
}

func TestBook_NoActorForbidden(t *testing.T) {
	svc, tsStore, _, _ := bookingFixtures()
	seedSlot(t, tsStore, "hosp-A", "slot-1", "doc-1")
	_, err := svc.Book(context.Background(), BookInput{DoctorID: "doc-1", TimeslotID: "slot-1"})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}
