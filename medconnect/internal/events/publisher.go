package events

import (
	"context"
	"sync"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
)

// Publisher appends events to the Store and fans them out to subscribers. It
// stamps a generated id and the current time on any event that lacks them, so
// callers only supply the meaningful fields (type, actor, entity, payload).
type Publisher struct {
	store *Store
	clock platform.Clock
	ids   platform.IDGen

	mu   sync.RWMutex
	subs []Subscriber
}

// NewPublisher wires a Publisher to its Store, clock, and id generator.
func NewPublisher(store *Store, clock platform.Clock, ids platform.IDGen) *Publisher {
	return &Publisher{store: store, clock: clock, ids: ids}
}

// Subscribe registers s to receive every subsequently published event.
func (p *Publisher) Subscribe(s Subscriber) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subs = append(p.subs, s)
}

// Publish stamps, stores, and fans out e, returning the enriched event so the
// caller can reference its id and timestamp.
func (p *Publisher) Publish(ctx context.Context, e domain.Event) domain.Event {
	if e.ID == "" {
		e.ID = p.ids.NewID()
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = p.clock.Now()
	}
	p.store.Append(e)

	// Snapshot subscribers so Notify runs without holding the lock.
	p.mu.RLock()
	subs := make([]Subscriber, len(p.subs))
	copy(subs, p.subs)
	p.mu.RUnlock()

	for _, s := range subs {
		s.Notify(ctx, e)
	}
	return e
}
