// Package events is the append-only event log at the heart of medconnect. Every
// state change is published here once; live updates (webhooks), historical
// overview, audit, and analytics are all built as consumers or queries over this
// single log, so producers never change when a new consumer is added.
package events

import (
	"context"
	"time"

	"medconnect/internal/domain"
)

// Subscriber receives events as they are published. Implementations MUST return
// quickly (e.g. enqueue onto a bounded queue) because fan-out is synchronous
// with Publish and shares the caller's request path.
type Subscriber interface {
	Notify(ctx context.Context, e domain.Event)
}

// Filter narrows an event Query. Zero-value fields match everything; the From/To
// bounds are inclusive so a point-in-time fold can pass To = "as of" time.
type Filter struct {
	Types     []domain.EventType
	EntityRef string
	From      time.Time
	To        time.Time
}

func (f Filter) matches(e domain.Event) bool {
	if f.EntityRef != "" && e.EntityRef != f.EntityRef {
		return false
	}
	if !f.From.IsZero() && e.Timestamp.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && e.Timestamp.After(f.To) {
		return false
	}
	if len(f.Types) > 0 {
		for _, t := range f.Types {
			if e.Type == t {
				return true
			}
		}
		return false
	}
	return true
}
