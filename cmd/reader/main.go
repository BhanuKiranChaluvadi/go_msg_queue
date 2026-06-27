// Command reader is the producer worker. It streams an input file to the queue
// broker as length-prefixed frames, in fixed-size chunks, without ever loading
// the whole file into memory.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"

	"filequeue/internal/cli"
	"filequeue/internal/wire"
)

// roleProducer mirrors the broker's role byte for a producer connection.
const roleProducer = 'P'

// chunkSize is the maximum payload carried by a single frame. It must not exceed
// wire.MaxFrameSize. 32 KiB balances syscall count against latency.
const chunkSize = 32 * 1024

// usageHeader and usageFooter make up the -h/-help text for this command.
const usageHeader = `reader — the filequeue producer worker.

It streams an input file to the queue broker as length-prefixed frames, in
fixed-size chunks, never loading the whole file into memory.

Usage:
  reader -in <file> [flags]

Flags:
`

const usageFooter = `
Examples:
  # Publish a file to the default stream on a local broker.
  reader -in input.txt

  # Publish to a named stream on a remote broker.
  reader -in photo.jpg -addr broker.internal:4000 -stream images
`

// copyFileToConn reads r in chunk-sized blocks and writes each block as a single
// frame to w, returning the number of frames written. It does not flush w; the
// caller owns the handshake and the final flush. A short final read is treated
// as a normal end of input, not an error.
func copyFileToConn(r io.Reader, w io.Writer, chunk int) (int, error) {
	buf := make([]byte, chunk)
	sent := 0
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			if werr := wire.WriteFrame(w, buf[:n]); werr != nil {
				return sent, werr
			}
			sent++
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return sent, nil
		}
		if err != nil {
			return sent, err
		}
	}
}

// runProducer performs the producer handshake on conn (role byte + stream id),
// reads the server's one-byte acknowledgement, and — only if accepted — streams
// src as frames via copyFileToConn and flushes. A busy ack aborts before any
// data is sent. All I/O is injected, so the sequence is testable without a
// socket.
func runProducer(conn io.ReadWriter, src io.Reader, stream string, chunk int) (int, error) {
	bw := bufio.NewWriter(conn)
	if err := bw.WriteByte(roleProducer); err != nil {
		return 0, err
	}
	if err := wire.WriteID(bw, stream); err != nil {
		return 0, err
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}

	ack, err := wire.ReadAck(conn)
	if err != nil {
		return 0, fmt.Errorf("read server ack: %w", err)
	}
	if ack != wire.AckOK {
		return 0, fmt.Errorf("server rejected stream %q (already in use)", stream)
	}

	sent, err := copyFileToConn(src, bw, chunk)
	if err != nil {
		return sent, err
	}
	if err := bw.Flush(); err != nil {
		return sent, err
	}
	return sent, nil
}

func main() {
	cli.SetUsage(usageHeader, usageFooter)
	input := flag.String("in", "", "input file path (required)")
	addr := flag.String("addr", "localhost:4000", "queue server address")
	stream := flag.String("stream", "default", "stream id to publish to")
	chunk := flag.Int("chunk", chunkSize, "bytes per frame (1..65535)")
	flag.Parse()
	cli.HandleExtraArgs()

	if *input == "" {
		log.Fatal("-in flag is required")
	}
	if *chunk < 1 || *chunk > wire.MaxFrameSize {
		log.Fatalf("-chunk must be between 1 and %d", wire.MaxFrameSize)
	}

	f, err := os.Open(*input)
	if err != nil {
		log.Fatalf("open input file: %v", err)
	}
	defer f.Close()

	conn, err := net.Dial("tcp", *addr)
	if err != nil {
		log.Fatalf("connect to queue server %s: %v", *addr, err)
	}
	defer conn.Close()

	sent, err := runProducer(conn, f, *stream, *chunk)
	if err != nil {
		log.Fatalf("stream input: %v", err)
	}
	log.Printf("reader done: sent %d frames to stream %q", sent, *stream)
}
