package events

import (
	"context"
	"sync"
	"testing"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
)

type captureSub struct {
	mu  sync.Mutex
	got []domain.Event
}

func (c *captureSub) Notify(_ context.Context, e domain.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, e)
}

func (c *captureSub) events() []domain.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]domain.Event, len(c.got))
	copy(out, c.got)
	return out
}

func TestStoreQueryFilters(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := NewStore()
	// Intentionally append out of timestamp order to prove Query sorts.
	s.Append(domain.Event{ID: "e2", TenantID: "A", Type: domain.EventNoteAdded, EntityRef: "appt1", Timestamp: base.Add(2 * time.Hour)})
	s.Append(domain.Event{ID: "e1", TenantID: "A", Type: domain.EventPrescriptionAdded, EntityRef: "appt1", Timestamp: base.Add(1 * time.Hour)})
	s.Append(domain.Event{ID: "e3", TenantID: "A", Type: domain.EventNoteAdded, EntityRef: "appt2", Timestamp: base.Add(3 * time.Hour)})
	s.Append(domain.Event{ID: "b1", TenantID: "B", Type: domain.EventNoteAdded, EntityRef: "appt1", Timestamp: base})

	// Ordered by timestamp across the whole tenant.
	all := s.Query("A", Filter{})
	if got := ids(all); !equal(got, []string{"e1", "e2", "e3"}) {
		t.Errorf("Query all order = %v, want [e1 e2 e3]", got)
	}

	// Filter by type.
	notes := s.Query("A", Filter{Types: []domain.EventType{domain.EventNoteAdded}})
	if got := ids(notes); !equal(got, []string{"e2", "e3"}) {
		t.Errorf("Query notes = %v, want [e2 e3]", got)
	}

	// Filter by entity.
	appt1 := s.Query("A", Filter{EntityRef: "appt1"})
	if got := ids(appt1); !equal(got, []string{"e1", "e2"}) {
		t.Errorf("Query appt1 = %v, want [e1 e2]", got)
	}

	// Inclusive To bound (point-in-time fold).
	upTo2h := s.Query("A", Filter{To: base.Add(2 * time.Hour)})
	if got := ids(upTo2h); !equal(got, []string{"e1", "e2"}) {
		t.Errorf("Query To=2h = %v, want [e1 e2]", got)
	}

	// Tenant isolation.
	if got := ids(s.Query("B", Filter{})); !equal(got, []string{"b1"}) {
		t.Errorf("Query B = %v, want [b1]", got)
	}
}

func TestPublisherStampsAndFansOut(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	clock := platform.NewFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	idgen := platform.NewFakeIDGen("ev-")
	pub := NewPublisher(store, clock, idgen)

	sub1, sub2 := &captureSub{}, &captureSub{}
	pub.Subscribe(sub1)
	pub.Subscribe(sub2)

	got := pub.Publish(ctx, domain.Event{TenantID: "A", Type: domain.EventNoteAdded, ActorID: "doc1", EntityRef: "appt1"})

	if got.ID != "ev-1" {
		t.Errorf("stamped ID = %q, want ev-1", got.ID)
	}
	if !got.Timestamp.Equal(clock.Now()) {
		t.Errorf("stamped Timestamp = %v, want %v", got.Timestamp, clock.Now())
	}
	// Appended to the store.
	if q := store.Query("A", Filter{}); len(q) != 1 || q[0].ID != "ev-1" {
		t.Errorf("store after publish = %v, want one event ev-1", ids(q))
	}
	// Both subscribers notified with the enriched event.
	for name, sub := range map[string]*captureSub{"sub1": sub1, "sub2": sub2} {
		evs := sub.events()
		if len(evs) != 1 || evs[0].ID != "ev-1" {
			t.Errorf("%s got %v, want [ev-1]", name, ids(evs))
		}
	}
}

func TestPublisherConcurrent(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	pub := NewPublisher(store, platform.NewFakeClock(time.Now()), platform.NewRandomID())
	sub := &captureSub{}
	pub.Subscribe(sub)

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pub.Publish(ctx, domain.Event{TenantID: "A", Type: domain.EventNoteAdded, EntityRef: "appt1"})
		}()
	}
	wg.Wait()

	if got := len(store.Query("A", Filter{})); got != n {
		t.Errorf("store has %d events, want %d", got, n)
	}
	if got := len(sub.events()); got != n {
		t.Errorf("subscriber saw %d events, want %d", got, n)
	}
}

func TestPublisherUnsubscribe(t *testing.T) {
	ctx := context.Background()
	pub := NewPublisher(NewStore(), platform.NewFakeClock(time.Now()), platform.NewFakeIDGen("ev-"))
	sub := &captureSub{}

	pub.Subscribe(sub)
	if pub.SubscriberCount() != 1 {
		t.Fatalf("count after subscribe = %d, want 1", pub.SubscriberCount())
	}
	pub.Publish(ctx, domain.Event{TenantID: "A", Type: domain.EventNoteAdded})

	pub.Unsubscribe(sub)
	if pub.SubscriberCount() != 0 {
		t.Fatalf("count after unsubscribe = %d, want 0", pub.SubscriberCount())
	}
	pub.Publish(ctx, domain.Event{TenantID: "A", Type: domain.EventNoteAdded})

	// Only the first publish reached the subscriber.
	if got := len(sub.events()); got != 1 {
		t.Errorf("subscriber saw %d events after unsubscribe, want 1", got)
	}
	// Unsubscribing an unknown subscriber is a no-op.
	pub.Unsubscribe(&captureSub{})
}

func ids(evs []domain.Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
