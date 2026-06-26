// Command writer is the consumer worker. It reads length-prefixed frames from
// the queue broker and appends their raw payloads to the output file, then
// flushes and fsyncs so the on-disk copy is complete before exit.
package main

import (
	"bufio"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"

	"filequeue/internal/wire"
)

// roleConsumer mirrors the broker's role byte for a consumer connection.
const roleConsumer = 'C'

func main() {
	output := flag.String("out", "", "output file path (required)")
	addr := flag.String("addr", "localhost:4000", "queue server address")
	stream := flag.String("stream", "default", "stream id to subscribe to")
	flag.Parse()

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

	bw := bufio.NewWriter(conn)
	if err := bw.WriteByte(roleConsumer); err != nil {
		log.Fatalf("write role: %v", err)
	}
	if err := wire.WriteID(bw, *stream); err != nil {
		log.Fatalf("write stream id: %v", err)
	}
	if err := bw.Flush(); err != nil {
		log.Fatalf("flush handshake: %v", err)
	}

	br := bufio.NewReader(conn)
	fw := bufio.NewWriter(f)

	written := 0
	for {
		payload, err := wire.ReadFrame(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			log.Fatalf("read frame: %v", err)
		}
		if _, err := fw.Write(payload); err != nil {
			log.Fatalf("write output: %v", err)
		}
		written++
	}

	if err := fw.Flush(); err != nil {
		log.Fatalf("flush output: %v", err)
	}
	if err := f.Sync(); err != nil {
		log.Fatalf("sync output: %v", err)
	}
	log.Printf("writer done: wrote %d frames from stream %q to %s", written, *stream, *output)
}
