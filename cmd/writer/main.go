// Command writer is the consumer worker. It reads length-prefixed frames from
// the queue broker and appends their raw payloads to the output file, then
// flushes and fsyncs so the on-disk copy is complete before exit.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"

	"filequeue/internal/cli"
	"filequeue/internal/wire"
)

// roleConsumer mirrors the broker's role byte for a consumer connection.
const roleConsumer = 'C'

// usageHeader and usageFooter make up the -h/-help text for this command.
const usageHeader = `writer — the filequeue consumer worker.

It reads length-prefixed frames for a stream from the queue broker and appends
their raw payloads to the output file, then flushes and fsyncs before exiting.

Usage:
  writer -out <file> [flags]

Flags:
`

const usageFooter = `
Examples:
  # Subscribe to the default stream and write to a file.
  writer -out output.txt

  # Subscribe to a named stream on a remote broker.
  writer -out photo.jpg -addr broker.internal:4000 -stream images
`

// copyConnToFile reads frames from r until EOF and writes each payload to w,
// returning the number of frames written. A clean EOF is success; any other
// read error is returned. It does not flush or sync w; the caller owns that.
func copyConnToFile(r io.Reader, w io.Writer) (int, error) {
	written := 0
	for {
		payload, err := wire.ReadFrame(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return written, nil
			}
			return written, err
		}
		if _, err := w.Write(payload); err != nil {
			return written, err
		}
		written++
	}
}

// runConsumer performs the consumer handshake on conn (role byte + stream id,
// flushed), reads the server's one-byte acknowledgement, and — only if accepted
// — drains frames into dst via copyConnToFile. A busy ack aborts before any
// data is read. All I/O is injected so the sequence is testable without a
// socket.
func runConsumer(conn io.ReadWriter, dst io.Writer, stream string) (int, error) {
	bw := bufio.NewWriter(conn)
	if err := bw.WriteByte(roleConsumer); err != nil {
		return 0, err
	}
	if err := wire.WriteID(bw, stream); err != nil {
		return 0, err
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}

	br := bufio.NewReader(conn)
	ack, err := wire.ReadAck(br)
	if err != nil {
		return 0, fmt.Errorf("read server ack: %w", err)
	}
	if ack != wire.AckOK {
		return 0, fmt.Errorf("server rejected stream %q (already in use)", stream)
	}

	return copyConnToFile(br, dst)
}

func main() {
	cli.SetUsage(usageHeader, usageFooter)
	output := flag.String("out", "", "output file path (required)")
	addr := flag.String("addr", "localhost:4000", "queue server address")
	stream := flag.String("stream", "default", "stream id to subscribe to")
	flag.Parse()
	cli.HandleExtraArgs()

	if *output == "" {
		log.Fatal("-out flag is required")
	}

	f, err := os.Create(*output)
	if err != nil {
		log.Fatalf("create output file: %v", err)
	}
	defer f.Close()

	conn, err := net.Dial("tcp", *addr)
	if err != nil {
		log.Fatalf("connect to queue server %s: %v", *addr, err)
	}
	defer conn.Close()

	fw := bufio.NewWriter(f)
	written, err := runConsumer(conn, fw, *stream)
	if err != nil {
		log.Fatalf("drain stream: %v", err)
	}

	if err := fw.Flush(); err != nil {
		log.Fatalf("flush output: %v", err)
	}
	if err := f.Sync(); err != nil {
		log.Fatalf("sync output: %v", err)
	}
	log.Printf("writer done: wrote %d frames from stream %q to %s", written, *stream, *output)
}
