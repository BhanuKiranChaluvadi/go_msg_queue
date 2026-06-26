// Command reader is the producer worker. It streams an input file to the queue
// broker as length-prefixed frames, in fixed-size chunks, without ever loading
// the whole file into memory.
package main

import (
	"bufio"
	"flag"
	"io"
	"log"
	"net"
	"os"

	"filequeue/internal/wire"
)

// roleProducer mirrors the broker's role byte for a producer connection.
const roleProducer = 'P'

// chunkSize is the maximum payload carried by a single frame. It must not exceed
// wire.MaxFrameSize. 32 KiB balances syscall count against latency.
const chunkSize = 32 * 1024

func main() {
	input := flag.String("in", "", "input file path (required)")
	addr := flag.String("addr", "localhost:4000", "queue server address")
	flag.Parse()

	if *input == "" {
		log.Fatal("-in flag is required")
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

	bw := bufio.NewWriter(conn)
	if err := bw.WriteByte(roleProducer); err != nil {
		log.Fatalf("write role: %v", err)
	}

	buf := make([]byte, chunkSize)
	sent := 0
	for {
		n, err := io.ReadFull(f, buf)
		if n > 0 {
			if werr := wire.WriteFrame(bw, buf[:n]); werr != nil {
				log.Fatalf("write frame: %v", werr)
			}
			sent++
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			log.Fatalf("read input: %v", err)
		}
	}

	if err := bw.Flush(); err != nil {
		log.Fatalf("flush: %v", err)
	}
	log.Printf("reader done: sent %d frames", sent)
}
