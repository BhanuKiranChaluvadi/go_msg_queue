package broker

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestProducerConsumerShareQueue(t *testing.T) {
	r := NewRegistry(4, 0)
	pq, err := r.AttachProducer("a")
	if err != nil {
		t.Fatalf("attach producer: %v", err)
	}
	cq, err := r.AttachConsumer("a")
	if err != nil {
		t.Fatalf("attach consumer: %v", err)
	}
	if pq != cq {
		t.Fatal("producer and consumer got different queues for the same id")
	}
	if got := r.Len(); got != 1 {
		t.Fatalf("Len = %d, want 1", got)
	}
}

func TestStreamsAreIsolated(t *testing.T) {
	r := NewRegistry(4, 0)
	qa, _ := r.AttachProducer("a")
	qb, _ := r.AttachProducer("b")
	if qa == qb {
		t.Fatal("different ids share a queue")
	}
	_ = qa.Push([]byte("a1"))
	_ = qb.Push([]byte("b1"))
	if got, _ := qa.Pop(); string(got) != "a1" {
		t.Errorf("stream a got %q, want a1", got)
	}
	if got, _ := qb.Pop(); string(got) != "b1" {
		t.Errorf("stream b got %q, want b1", got)
	}
}

func TestSingleProducerPerStream(t *testing.T) {
	r := NewRegistry(4, 0)
	if _, err := r.AttachProducer("a"); err != nil {
		t.Fatalf("first producer: %v", err)
	}
	if _, err := r.AttachProducer("a"); !errors.Is(err, ErrProducerExists) {
		t.Errorf("second producer: got %v, want ErrProducerExists", err)
	}
}

func TestProducerCloseDrains(t *testing.T) {
	r := NewRegistry(4, 0)
	q, _ := r.AttachProducer("a")
	_ = q.Push([]byte("x"))
	r.DetachProducer("a")

	if got, ok := q.Pop(); !ok || string(got) != "x" {
		t.Fatalf("drain buffered: got %q ok=%v", got, ok)
	}
	if _, ok := q.Pop(); ok {
		t.Fatal("queue not closed after the producer detached")
	}
}

func TestLateConsumerDrainsThenGC(t *testing.T) {
	r := NewRegistry(4, 0)
	q, _ := r.AttachProducer("a")
	_ = q.Push([]byte("buffered"))
	r.DetachProducer("a")

	if got := r.Len(); got != 1 {
		t.Fatalf("stream removed before consume; Len=%d", got)
	}
	cq, err := r.AttachConsumer("a")
	if err != nil {
		t.Fatalf("attach consumer: %v", err)
	}
	if got, ok := cq.Pop(); !ok || string(got) != "buffered" {
		t.Fatalf("drain: got %q ok=%v", got, ok)
	}
	if _, ok := cq.Pop(); ok {
		t.Fatal("expected closed after drain")
	}
	r.DetachConsumer("a")
	if got := r.Len(); got != 0 {
		t.Fatalf("stream not GC'd after consume; Len=%d", got)
	}
}

func TestSingleConsumerPerStream(t *testing.T) {
	r := NewRegistry(4, 0)
	if _, err := r.AttachConsumer("a"); err != nil {
		t.Fatalf("first consumer: %v", err)
	}
	if _, err := r.AttachConsumer("a"); !errors.Is(err, ErrConsumerExists) {
		t.Errorf("second consumer: got %v, want ErrConsumerExists", err)
	}
}

func TestMaxStreams(t *testing.T) {
	r := NewRegistry(4, 1)
	if _, err := r.AttachProducer("a"); err != nil {
		t.Fatalf("first stream: %v", err)
	}
	if _, err := r.AttachProducer("b"); !errors.Is(err, ErrTooManyStreams) {
		t.Errorf("second stream: got %v, want ErrTooManyStreams", err)
	}
}

func TestWaitReady(t *testing.T) {
	r := NewRegistry(4, 0)
	if _, err := r.AttachConsumer("a"); err != nil {
		t.Fatalf("attach consumer: %v", err)
	}
	if r.WaitReady("a", 50*time.Millisecond) {
		t.Fatal("WaitReady returned true with no producer")
	}
	if _, err := r.AttachProducer("a"); err != nil {
		t.Fatalf("attach producer: %v", err)
	}
	if !r.WaitReady("a", 50*time.Millisecond) {
		t.Fatal("WaitReady returned false after a producer attached")
	}
}

func TestWaitReadyUnknownStream(t *testing.T) {
	r := NewRegistry(4, 0)
	if r.WaitReady("nope", 10*time.Millisecond) {
		t.Fatal("WaitReady returned true for an unknown stream")
	}
}

func TestConcurrentDistinctStreams(t *testing.T) {
	r := NewRegistry(8, 0)
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		id := "stream-" + strconv.Itoa(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			q, err := r.AttachProducer(id)
			if err != nil {
				t.Errorf("attach producer %s: %v", id, err)
				return
			}
			_ = q.Push([]byte("x"))
			r.DetachProducer(id)

			cq, err := r.AttachConsumer(id)
			if err != nil {
				t.Errorf("attach consumer %s: %v", id, err)
				return
			}
			if _, ok := cq.Pop(); !ok {
				t.Errorf("%s: expected one buffered frame", id)
			}
			if _, ok := cq.Pop(); ok {
				t.Errorf("%s: expected closed after drain", id)
			}
			r.DetachConsumer(id)
		}()
	}
	wg.Wait()

	if got := r.Len(); got != 0 {
		t.Fatalf("Len=%d after all streams consumed, want 0", got)
	}
}
