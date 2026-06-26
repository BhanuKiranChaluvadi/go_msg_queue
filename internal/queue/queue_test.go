package queue

import (
	"errors"
	"testing"
	"time"
)

func TestPushPopPreservesOrder(t *testing.T) {
	q := New(4)
	for i := 0; i < 4; i++ {
		if err := q.Push([]byte{byte(i)}); err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
	}
	for i := 0; i < 4; i++ {
		p, ok := q.Pop()
		if !ok || len(p) != 1 || p[0] != byte(i) {
			t.Fatalf("Pop %d: got %v ok=%v", i, p, ok)
		}
	}
}

func TestPopBlocksUntilPush(t *testing.T) {
	q := New(1)
	got := make(chan []byte, 1)
	go func() {
		p, _ := q.Pop()
		got <- p
	}()

	select {
	case <-got:
		t.Fatal("Pop returned before any Push")
	case <-time.After(20 * time.Millisecond):
	}

	if err := q.Push([]byte("x")); err != nil {
		t.Fatalf("Push: %v", err)
	}
	select {
	case p := <-got:
		if string(p) != "x" {
			t.Fatalf("got %q, want x", p)
		}
	case <-time.After(time.Second):
		t.Fatal("Pop did not return after Push")
	}
}

func TestPushBlocksWhenFull(t *testing.T) {
	q := New(1)
	if err := q.Push([]byte("a")); err != nil {
		t.Fatalf("Push a: %v", err)
	}

	pushed := make(chan struct{})
	go func() {
		_ = q.Push([]byte("b")) // must block until a is popped
		close(pushed)
	}()

	select {
	case <-pushed:
		t.Fatal("Push returned while queue was full")
	case <-time.After(20 * time.Millisecond):
	}

	if _, ok := q.Pop(); !ok {
		t.Fatal("Pop a: not ok")
	}
	select {
	case <-pushed:
	case <-time.After(time.Second):
		t.Fatal("Push did not unblock after Pop")
	}
}

func TestCloseDrainsThenSignals(t *testing.T) {
	q := New(4)
	_ = q.Push([]byte("a"))
	_ = q.Push([]byte("b"))
	q.Close()

	if p, ok := q.Pop(); !ok || string(p) != "a" {
		t.Fatalf("drain 1: got %q ok=%v", p, ok)
	}
	if p, ok := q.Pop(); !ok || string(p) != "b" {
		t.Fatalf("drain 2: got %q ok=%v", p, ok)
	}
	if _, ok := q.Pop(); ok {
		t.Fatal("expected (nil,false) after drain on closed queue")
	}
}

func TestPushAfterCloseReturnsErrClosed(t *testing.T) {
	q := New(1)
	q.Close()
	if err := q.Push([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Push after close: got %v, want ErrClosed", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	q := New(1)
	q.Close()
	q.Close() // must not panic
}
