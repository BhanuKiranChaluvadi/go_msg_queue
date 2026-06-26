package main

import (
	"bufio"
	"bytes"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"filequeue/internal/broker"
	"filequeue/internal/queue"
	"filequeue/internal/wire"
)

func TestPumpInCopiesFramesThenStopsOnEOF(t *testing.T) {
	var src bytes.Buffer
	for _, p := range [][]byte{[]byte("one"), []byte("two"), []byte("three")} {
		if err := wire.WriteFrame(&src, p); err != nil {
			t.Fatalf("seed frame: %v", err)
		}
	}

	q := queue.New(8)
	if err := pumpIn(&src, q, nil); err != nil {
		t.Fatalf("pumpIn: %v", err)
	}
	q.Close()

	want := []string{"one", "two", "three"}
	for _, w := range want {
		got, ok := q.Pop()
		if !ok || string(got) != w {
			t.Fatalf("Pop = %q ok=%v, want %q", got, ok, w)
		}
	}
	if _, ok := q.Pop(); ok {
		t.Fatal("expected queue drained after pumpIn + Close")
	}
}

func TestPumpInRefreshesDeadlineBeforeEachFrame(t *testing.T) {
	var src bytes.Buffer
	_ = wire.WriteFrame(&src, []byte("a"))
	_ = wire.WriteFrame(&src, []byte("b"))

	calls := 0
	q := queue.New(4)
	if err := pumpIn(&src, q, func() { calls++ }); err != nil {
		t.Fatalf("pumpIn: %v", err)
	}
	// One refresh before each of the two frames, plus one before the read that
	// observes EOF.
	if calls != 3 {
		t.Fatalf("beforeFrame called %d times, want 3", calls)
	}
}

func TestPumpInReturnsErrorOnTruncatedFrame(t *testing.T) {
	// Header declares 5 bytes but only 2 follow: a truncated frame.
	src := bytes.NewReader([]byte{0, 0, 0, 5, 'a', 'b'})
	q := queue.New(4)
	if err := pumpIn(src, q, nil); err == nil {
		t.Fatal("expected an error for a truncated frame, got nil")
	}
}

func TestPumpOutWritesBufferedFramesThenFlushes(t *testing.T) {
	q := queue.New(4)
	_ = q.Push([]byte("alpha"))
	_ = q.Push([]byte("beta"))
	q.Close()

	var dst bytes.Buffer
	if err := pumpOut(&dst, q, nil); err != nil {
		t.Fatalf("pumpOut: %v", err)
	}

	r := bytes.NewReader(dst.Bytes())
	for _, w := range []string{"alpha", "beta"} {
		got, err := wire.ReadFrame(r)
		if err != nil || string(got) != w {
			t.Fatalf("ReadFrame = %q err=%v, want %q", got, err, w)
		}
	}
	if _, err := wire.ReadFrame(r); err == nil {
		t.Fatal("expected EOF after the buffered frames")
	}
}

func TestPumpOutEmptyClosedQueueFlushesNothing(t *testing.T) {
	q := queue.New(4)
	q.Close()

	var dst bytes.Buffer
	if err := pumpOut(&dst, q, nil); err != nil {
		t.Fatalf("pumpOut: %v", err)
	}
	if dst.Len() != 0 {
		t.Fatalf("wrote %d bytes for an empty stream, want 0", dst.Len())
	}
}

// TestHandleConnRoundTrip drives a producer and then a consumer through
// handleConn over in-memory pipes and asserts the frames arrive in order.
func TestHandleConnRoundTrip(t *testing.T) {
	reg := broker.NewRegistry(8, 0)

	// Producer: send role + id + frames, then close so the stream completes.
	pSrv, pCli := net.Pipe()
	pDone := make(chan struct{})
	go func() { handleConn(pSrv, reg, 0, time.Second); close(pDone) }()
	go func() {
		bw := bufio.NewWriter(pCli)
		_ = bw.WriteByte(roleProducer)
		_ = wire.WriteID(bw, "s")
		_ = wire.WriteFrame(bw, []byte("hello"))
		_ = wire.WriteFrame(bw, []byte("world"))
		_ = bw.Flush()
		_ = pCli.Close()
	}()
	select {
	case <-pDone:
	case <-time.After(2 * time.Second):
		t.Fatal("producer handler did not finish")
	}

	// Consumer: drains the two buffered frames for the same stream.
	cSrv, cCli := net.Pipe()
	go handleConn(cSrv, reg, 0, time.Second)
	cbw := bufio.NewWriter(cCli)
	_ = cbw.WriteByte(roleConsumer)
	_ = wire.WriteID(cbw, "s")
	_ = cbw.Flush()

	_ = cCli.SetReadDeadline(time.Now().Add(2 * time.Second))
	cr := bufio.NewReader(cCli)
	for _, w := range []string{"hello", "world"} {
		got, err := wire.ReadFrame(cr)
		if err != nil || string(got) != w {
			t.Fatalf("consumer ReadFrame = %q err=%v, want %q", got, err, w)
		}
	}
	_ = cCli.Close()
}

// TestHandleConsumerAttachTimeout asserts a consumer for a stream with no
// producer gives up after attachTimeout instead of blocking forever.
func TestHandleConsumerAttachTimeout(t *testing.T) {
	reg := broker.NewRegistry(8, 0)
	srv, cli := net.Pipe()
	done := make(chan struct{})
	go func() { handleConn(srv, reg, 0, 50*time.Millisecond); close(done) }()

	bw := bufio.NewWriter(cli)
	_ = bw.WriteByte(roleConsumer)
	_ = wire.WriteID(bw, "ghost")
	_ = bw.Flush()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer handler did not return after the attach timeout")
	}
	_ = cli.Close()
}

// TestServeRoundTripAndGracefulStop runs the real accept loop on a loopback
// listener: a producer and consumer connect over TCP, a frame round-trips, and
// the loop returns cleanly once closing is set and the listener is closed.
func TestServeRoundTripAndGracefulStop(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	reg := broker.NewRegistry(8, 0)
	var closing atomic.Bool
	done := make(chan struct{})
	go func() {
		serve(ln, reg, serverConfig{idle: 0, maxConns: 2, attachTimeout: time.Second}, &closing)
		close(done)
	}()
	addr := ln.Addr().String()

	// Producer connects and sends one frame; left open until the consumer reads.
	pc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial producer: %v", err)
	}
	pbw := bufio.NewWriter(pc)
	_ = pbw.WriteByte(roleProducer)
	_ = wire.WriteID(pbw, "s")
	_ = wire.WriteFrame(pbw, []byte("payload"))
	if err := pbw.Flush(); err != nil {
		t.Fatalf("flush producer: %v", err)
	}
	// Closing the producer ends the stream; its frame stays buffered for the
	// consumer, and pumpOut flushes once the queue is closed and drained.
	_ = pc.Close()

	// Consumer connects, drains the buffered frame, then sees EOF.
	cc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial consumer: %v", err)
	}
	cbw := bufio.NewWriter(cc)
	_ = cbw.WriteByte(roleConsumer)
	_ = wire.WriteID(cbw, "s")
	if err := cbw.Flush(); err != nil {
		t.Fatalf("flush consumer: %v", err)
	}
	_ = cc.SetReadDeadline(time.Now().Add(2 * time.Second))
	cr := bufio.NewReader(cc)
	got, err := wire.ReadFrame(cr)
	if err != nil || string(got) != "payload" {
		t.Fatalf("consumer ReadFrame = %q err=%v, want %q", got, err, "payload")
	}
	if _, err := wire.ReadFrame(cr); err == nil {
		t.Fatal("expected EOF after the stream drained")
	}
	_ = cc.Close()

	// Trigger graceful shutdown and assert serve returns.
	closing.Store(true)
	_ = ln.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not return after shutdown")
	}
}
