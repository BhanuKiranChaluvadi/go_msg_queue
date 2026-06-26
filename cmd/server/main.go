// Command server is the queue broker. It accepts TCP connections, each of which
// declares a role with a single leading byte ('P' producer, 'C' consumer), then
// moves length-prefixed frames from producers into a bounded in-memory FIFO and
// out to the consumer in order. Standard library only.
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

	"filequeue/internal/queue"
	"filequeue/internal/wire"
)

// Connection roles, sent as the first byte of every connection.
const (
	roleProducer = 'P'
	roleConsumer = 'C'
)

// queueCapacity is the number of frames buffered before Push blocks. Combined
// with wire.MaxFrameSize it bounds broker memory: cap * MaxFrameSize.
const queueCapacity = 1024

func main() {
	addr := flag.String("addr", "127.0.0.1:4000", "TCP listen address")
	idle := flag.Duration("idle", 30*time.Second, "per-connection idle read timeout; 0 disables")
	flag.Parse()

	q := queue.New(queueCapacity)

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
		ln.Close() // unblock Accept
		q.Close()  // let the consumer drain and finish
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if closing.Load() {
				return
			}
			log.Printf("accept: %v", err)
			continue
		}
		go handleConn(conn, q, *idle)
	}
}

// touchDeadline refreshes the connection's read deadline to now+idle. A zero
// idle disables the deadline. Refreshing before each read makes it a rolling
// idle timeout: a connection making forward progress is never cut off, but one
// that stalls (slowloris) is dropped after idle.
func touchDeadline(conn net.Conn, idle time.Duration) {
	if idle <= 0 {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(idle))
}

// handleConn dispatches a single connection based on its declared role. A
// recovered panic is confined to this connection so the accept loop survives.
func handleConn(conn net.Conn, q *queue.Queue, idle time.Duration) {
	defer conn.Close()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("recovered from panic on %s: %v", conn.RemoteAddr(), r)
		}
	}()

	br := bufio.NewReader(conn)
	touchDeadline(conn, idle)
	role, err := br.ReadByte()
	if err != nil {
		log.Printf("read role: %v", err)
		return
	}

	switch role {
	case roleProducer:
		handleProducer(conn, br, q, idle)
	case roleConsumer:
		handleConsumer(conn, q)
	default:
		log.Printf("unknown role byte %q from %s", role, conn.RemoteAddr())
	}
}

// handleProducer reads frames until the producer half-closes, then closes the
// queue to signal the consumer that the stream is complete. The read deadline
// is refreshed before each frame so a stalled producer is dropped without
// penalising a slow-but-progressing one.
func handleProducer(conn net.Conn, br *bufio.Reader, q *queue.Queue, idle time.Duration) {
	for {
		touchDeadline(conn, idle)
		payload, err := wire.ReadFrame(br)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("producer read: %v", err)
			}
			break
		}
		if err := q.Push(payload); err != nil {
			log.Printf("producer push: %v", err)
			break
		}
	}
	q.Close()
}

// handleConsumer writes frames to the consumer until the queue is closed and
// drained, then flushes.
func handleConsumer(conn net.Conn, q *queue.Queue) {
	bw := bufio.NewWriter(conn)
	for {
		payload, ok := q.Pop()
		if !ok {
			break
		}
		if err := wire.WriteFrame(bw, payload); err != nil {
			log.Printf("consumer write: %v", err)
			return
		}
	}
	if err := bw.Flush(); err != nil {
		log.Printf("consumer flush: %v", err)
	}
}
