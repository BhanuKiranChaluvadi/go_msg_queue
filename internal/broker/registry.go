// Package broker provides the stream registry that routes producers and
// consumers to per-stream FIFOs. Each stream id maps to its own bounded queue,
// so independent streams neither interleave nor truncate one another. A stream's
// queue is closed when its last producer detaches, and the entry is removed once
// the stream has been fully consumed, keeping the map bounded.
package broker

import (
	"errors"
	"sync"
	"time"

	"filequeue/internal/queue"
)

var (
	// ErrTooManyStreams is returned when creating a new stream would exceed the
	// configured cap.
	ErrTooManyStreams = errors.New("broker: max streams reached")
	// ErrProducerExists is returned when a second producer attaches to a stream
	// that already has (or had) one. Streams are single-producer by design.
	ErrProducerExists = errors.New("broker: stream already has a producer")
	// ErrConsumerExists is returned when a second consumer attaches to a stream
	// that already has one. Streams are single-consumer by design.
	ErrConsumerExists = errors.New("broker: stream already has a consumer")
)

type stream struct {
	q            *queue.Queue
	ready        chan struct{} // closed when the producer attaches
	hasProducer  bool
	producerDone bool
	hasConsumer  bool
	consumed     bool
}

// Registry maps stream identifiers to per-stream queues with reference-counted
// lifecycle. It is safe for concurrent use.
type Registry struct {
	mu         sync.Mutex
	streams    map[string]*stream
	capacity   int
	maxStreams int
}

// NewRegistry returns a Registry whose per-stream queues buffer capacity frames
// and which permits at most maxStreams concurrent streams (0 means unlimited).
func NewRegistry(capacity, maxStreams int) *Registry {
	return &Registry{
		streams:    make(map[string]*stream),
		capacity:   capacity,
		maxStreams: maxStreams,
	}
}

// getOrCreateLocked returns the stream for id, creating it if absent. It returns
// ErrTooManyStreams if a new stream would exceed the cap.
func (r *Registry) getOrCreateLocked(id string) (*stream, error) {
	if s := r.streams[id]; s != nil {
		return s, nil
	}
	if r.maxStreams > 0 && len(r.streams) >= r.maxStreams {
		return nil, ErrTooManyStreams
	}
	s := &stream{
		q:     queue.New(r.capacity),
		ready: make(chan struct{}),
	}
	r.streams[id] = s
	return s, nil
}

// AttachProducer registers the single producer on id and returns its queue,
// creating the stream if necessary. It returns ErrProducerExists if the stream
// already has, or has already had, a producer.
func (r *Registry) AttachProducer(id string) (*queue.Queue, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, err := r.getOrCreateLocked(id)
	if err != nil {
		return nil, err
	}
	if s.hasProducer || s.producerDone {
		return nil, ErrProducerExists
	}
	s.hasProducer = true
	close(s.ready)
	return s.q, nil
}

// DetachProducer releases the producer on id, closing the stream's queue so the
// consumer can drain and finish.
func (r *Registry) DetachProducer(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.streams[id]
	if s == nil || !s.hasProducer {
		return
	}
	s.hasProducer = false
	s.producerDone = true
	s.q.Close()
	r.gcLocked(id, s)
}

// AttachConsumer registers the single consumer on id and returns its queue,
// creating the stream if necessary. It returns ErrConsumerExists if a consumer
// is already attached.
func (r *Registry) AttachConsumer(id string) (*queue.Queue, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, err := r.getOrCreateLocked(id)
	if err != nil {
		return nil, err
	}
	if s.hasConsumer {
		return nil, ErrConsumerExists
	}
	s.hasConsumer = true
	return s.q, nil
}

// DetachConsumer releases the consumer on id, marking the stream consumed so a
// fully drained stream is removed.
func (r *Registry) DetachConsumer(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.streams[id]
	if s == nil {
		return
	}
	s.hasConsumer = false
	s.consumed = true
	r.gcLocked(id, s)
}

// WaitReady blocks until a producer has attached to id or timeout elapses,
// returning true if the stream became ready. A non-positive timeout waits
// indefinitely. It returns false if id is unknown. A consumer uses this to bound
// how long it waits for an absent producer.
func (r *Registry) WaitReady(id string, timeout time.Duration) bool {
	r.mu.Lock()
	s := r.streams[id]
	r.mu.Unlock()
	if s == nil {
		return false
	}
	if timeout <= 0 {
		<-s.ready
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-s.ready:
		return true
	case <-timer.C:
		return false
	}
}

// Len returns the number of live streams. Intended for tests and metrics.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.streams)
}

// gcLocked removes a stream once it has no producer, no consumer, and has been
// consumed, preventing the map from growing without bound.
func (r *Registry) gcLocked(id string, s *stream) {
	if !s.hasProducer && !s.hasConsumer && s.consumed {
		delete(r.streams, id)
	}
}

// CloseAll closes every live stream's queue so consumers drain and finish. It is
// used during graceful shutdown.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.streams {
		s.q.Close()
	}
}
