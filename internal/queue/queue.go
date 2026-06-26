// Package queue provides a bounded, blocking, closeable FIFO of byte-slice
// frames. It is the broker's in-memory buffer: producers Push and a consumer
// Pop. The fixed capacity gives the broker a flat, predictable memory footprint
// and provides backpressure — Push blocks once the queue is full, which in turn
// stalls the producer's TCP socket.
//
// Ownership: Push does not copy the payload, so the caller must not reuse the
// slice after a successful Push. The reader allocates a fresh slice per frame,
// so this is naturally satisfied.
package queue

import (
	"errors"
	"sync"
)

// ErrClosed is returned by Push after the queue has been closed.
var ErrClosed = errors.New("queue: closed")

// Queue is a bounded FIFO of frames safe for concurrent use by multiple
// producers and a consumer.
type Queue struct {
	ch      chan []byte
	closing chan struct{}
	once    sync.Once
}

// New returns a Queue that buffers up to capacity frames before Push blocks.
func New(capacity int) *Queue {
	return &Queue{
		ch:      make(chan []byte, capacity),
		closing: make(chan struct{}),
	}
}

// Push appends a frame, blocking while the queue is full. It returns ErrClosed
// if the queue is closed.
func (q *Queue) Push(payload []byte) error {
	select {
	case q.ch <- payload:
		return nil
	case <-q.closing:
		return ErrClosed
	}
}

// Pop removes and returns the oldest frame, blocking while the queue is empty.
// Once the queue is closed and all buffered frames have been drained, Pop
// returns (nil, false).
func (q *Queue) Pop() ([]byte, bool) {
	// Fast path: drain any already-buffered frame before observing closure, so
	// closing never causes buffered frames to be dropped.
	select {
	case p := <-q.ch:
		return p, true
	default:
	}

	select {
	case p := <-q.ch:
		return p, true
	case <-q.closing:
		select {
		case p := <-q.ch:
			return p, true
		default:
			return nil, false
		}
	}
}

// Close marks the queue closed. It is idempotent and safe to call concurrently.
// Buffered frames remain available to Pop until drained.
func (q *Queue) Close() {
	q.once.Do(func() { close(q.closing) })
}
