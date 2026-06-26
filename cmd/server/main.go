// Command server is the queue broker. It accepts TCP connections, each of which
// declares a role ('P' producer, 'C' consumer) and a stream id, then moves
// length-prefixed frames from each producer into a per-stream bounded FIFO and
// out to that stream's consumer in order. Standard library only.
package main

import (
	"bufio"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"filequeue/internal/broker"
	"filequeue/internal/queue"
	"filequeue/internal/wire"
)

// Connection roles, sent as the first byte of every connection.
const (
	roleProducer = 'P'
	roleConsumer = 'C'
)

// queueCapacity is the number of frames buffered per stream before Push blocks.
const queueCapacity = 1024

func main() {
	addr := flag.String("addr", "127.0.0.1:4000", "TCP listen address")
	idle := flag.Duration("idle", 30*time.Second, "per-connection idle read/write timeout; 0 disables")
	maxStreams := flag.Int("max-streams", 256, "maximum concurrent streams; 0 means unlimited")
	maxConns := flag.Int("max-conns", 1024, "maximum concurrent connections; 0 means unlimited")
	attachTimeout := flag.Duration("attach-timeout", 10*time.Second, "how long a consumer waits for an absent producer; 0 waits forever")
	flag.Parse()

	reg := broker.NewRegistry(queueCapacity, *maxStreams)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("queue server listening on %s", *addr)

	var closing atomic.Bool
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Print("shutdown signal received: draining")
		closing.Store(true)
		ln.Close()     // unblock Accept
		reg.CloseAll() // let consumers drain and finish
	}()

	serve(ln, reg, serverConfig{idle: *idle, maxConns: *maxConns, attachTimeout: *attachTimeout}, &closing)
}

// serverConfig holds the per-connection tunables the accept loop passes down.
type serverConfig struct {
	idle          time.Duration
	maxConns      int
	attachTimeout time.Duration
}

// serve runs the accept loop on ln, dispatching each connection to handleConn in
// its own goroutine and capping concurrency at cfg.maxConns (0 = unlimited). It
// returns when Accept fails after closing has been set; callers trigger a
// graceful stop by storing true into closing and closing ln.
func serve(ln net.Listener, reg *broker.Registry, cfg serverConfig, closing *atomic.Bool) {
	// sem caps concurrent connections so a flood cannot exhaust goroutines or
	// file descriptors. A nil sem means unlimited.
	var sem chan struct{}
	if cfg.maxConns > 0 {
		sem = make(chan struct{}, cfg.maxConns)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			if closing.Load() {
				return
			}
			log.Printf("accept: %v", err)
			continue
		}
		if sem != nil {
			sem <- struct{}{} // blocks once max-conns are in flight
		}
		go func() {
			if sem != nil {
				defer func() { <-sem }()
			}
			handleConn(conn, reg, cfg.idle, cfg.attachTimeout)
		}()
	}
}

// touchReadDeadline refreshes the connection's read deadline to now+idle. A zero
// idle disables the deadline. Refreshing before each read makes it a rolling
// idle timeout: a connection making forward progress is never cut off, but one
// that stalls (slowloris) is dropped after idle.
func touchReadDeadline(conn net.Conn, idle time.Duration) {
	if idle <= 0 {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(idle))
}

// touchWriteDeadline refreshes the connection's write deadline to now+idle. It
// bounds a slow- or non-reading consumer (a slow-read attack): once the kernel
// send buffer fills, Write would otherwise block forever, pinning the goroutine
// and the stream's queue and stalling the producer. With the deadline the write
// fails after idle, the consumer detaches, and the stream is freed.
func touchWriteDeadline(conn net.Conn, idle time.Duration) {
	if idle <= 0 {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(idle))
}

// handleConn dispatches a single connection based on its declared role and
// stream id. A recovered panic is confined to this connection so the accept loop
// survives.
func handleConn(conn net.Conn, reg *broker.Registry, idle, attachTimeout time.Duration) {
	defer conn.Close()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("recovered from panic on %s: %v", conn.RemoteAddr(), r)
		}
	}()

	br := bufio.NewReader(conn)
	touchReadDeadline(conn, idle)
	role, err := br.ReadByte()
	if err != nil {
		log.Printf("read role: %v", err)
		return
	}
	touchReadDeadline(conn, idle)
	id, err := wire.ReadID(br)
	if err != nil {
		log.Printf("read stream id from %s: %v", conn.RemoteAddr(), err)
		return
	}

	switch role {
	case roleProducer:
		handleProducer(conn, br, reg, id, idle)
	case roleConsumer:
		handleConsumer(conn, reg, id, idle, attachTimeout)
	default:
		log.Printf("unknown role byte %q from %s", role, conn.RemoteAddr())
	}
}

// handleProducer attaches the producer for id, pumps its frames into the stream
// queue, and detaches on exit (which closes the queue so the consumer finishes).
func handleProducer(conn net.Conn, br *bufio.Reader, reg *broker.Registry, id string, idle time.Duration) {
	q, err := reg.AttachProducer(id)
	if err != nil {
		log.Printf("producer %q: %v", id, err)
		return
	}
	defer reg.DetachProducer(id)

	if err := pumpIn(br, q, func() { touchReadDeadline(conn, idle) }); err != nil {
		log.Printf("producer %q: %v", id, err)
	}
}

// handleConsumer attaches the consumer for id, waits up to attachTimeout for a
// producer, then pumps the stream's frames out to the connection.
func handleConsumer(conn net.Conn, reg *broker.Registry, id string, idle, attachTimeout time.Duration) {
	q, err := reg.AttachConsumer(id)
	if err != nil {
		log.Printf("consumer %q: %v", id, err)
		return
	}
	defer reg.DetachConsumer(id)

	if !reg.WaitReady(id, attachTimeout) {
		log.Printf("consumer %q: no producer within %s", id, attachTimeout)
		return
	}

	if err := pumpOut(conn, q, func() { touchWriteDeadline(conn, idle) }); err != nil {
		log.Printf("consumer %q: %v", id, err)
	}
}

// pumpIn copies frames from r into q until r reaches a clean EOF, calling
// beforeFrame (if non-nil) to refresh the read deadline before each frame. It is
// transport-agnostic so it can be unit-tested against any io.Reader.
func pumpIn(r io.Reader, q *queue.Queue, beforeFrame func()) error {
	for {
		if beforeFrame != nil {
			beforeFrame()
		}
		payload, err := wire.ReadFrame(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := q.Push(payload); err != nil {
			return err
		}
	}
}

// pumpOut copies frames from q to w until q is closed and drained, calling
// beforeFrame (if non-nil) to refresh the write deadline before each frame, then
// flushes. It is transport-agnostic so it can be unit-tested against any
// io.Writer.
func pumpOut(w io.Writer, q *queue.Queue, beforeFrame func()) error {
	bw := bufio.NewWriter(w)
	for {
		payload, ok := q.Pop()
		if !ok {
			break
		}
		if beforeFrame != nil {
			beforeFrame()
		}
		if err := wire.WriteFrame(bw, payload); err != nil {
			return err
		}
	}
	if beforeFrame != nil {
		beforeFrame()
	}
	return bw.Flush()
}
