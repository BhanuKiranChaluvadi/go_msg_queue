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

// runConsumer performs the consumer handshake on handshake (role byte + stream
// id, flushed) and then drains frames into dst via copyConnToFile. All I/O is
// injected so the full protocol sequence is testable without a socket; in main
// the handshake and frames writers are the same connection.
func runConsumer(handshake io.Writer, frames io.Reader, dst io.Writer, stream string) (int, error) {
	bw := bufio.NewWriter(handshake)
	if err := bw.WriteByte(roleConsumer); err != nil {
		return 0, err
	}
	if err := wire.WriteID(bw, stream); err != nil {
		return 0, err
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	return copyConnToFile(bufio.NewReader(frames), dst)
}

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

	fw := bufio.NewWriter(f)
	written, err := runConsumer(conn, conn, fw, *stream)
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
