package main

import (
	"bytes"
	"testing"

	"filequeue/internal/wire"
)

func TestCopyConnToFileReassemblesPayloads(t *testing.T) {
	payloads := [][]byte{[]byte("foo"), []byte("bar"), []byte("baz")}
	var conn bytes.Buffer
	for _, p := range payloads {
		if err := wire.WriteFrame(&conn, p); err != nil {
			t.Fatalf("seed frame: %v", err)
		}
	}

	var out bytes.Buffer
	n, err := copyConnToFile(&conn, &out)
	if err != nil {
		t.Fatalf("copyConnToFile: %v", err)
	}
	if n != len(payloads) {
		t.Fatalf("wrote %d frames, want %d", n, len(payloads))
	}
	if got := out.String(); got != "foobarbaz" {
		t.Fatalf("reassembled %q, want %q", got, "foobarbaz")
	}
}

func TestCopyConnToFileEmptyStream(t *testing.T) {
	var conn bytes.Buffer // no frames at all
	var out bytes.Buffer
	n, err := copyConnToFile(&conn, &out)
	if err != nil {
		t.Fatalf("copyConnToFile: %v", err)
	}
	if n != 0 || out.Len() != 0 {
		t.Fatalf("got %d frames / %d bytes, want 0 / 0", n, out.Len())
	}
}

func TestCopyConnToFilePropagatesTruncatedFrame(t *testing.T) {
	// Header claims 5 bytes; only 2 follow.
	conn := bytes.NewReader([]byte{0, 0, 0, 5, 'a', 'b'})
	var out bytes.Buffer
	if _, err := copyConnToFile(conn, &out); err == nil {
		t.Fatal("expected a truncated-frame error")
	}
}

func TestRunConsumerHandshakeThenReassembles(t *testing.T) {
	var frames bytes.Buffer
	for _, p := range [][]byte{[]byte("ab"), []byte("cd")} {
		if err := wire.WriteFrame(&frames, p); err != nil {
			t.Fatalf("seed frame: %v", err)
		}
	}

	var handshake, out bytes.Buffer
	n, err := runConsumer(&handshake, &frames, &out, "s1")
	if err != nil {
		t.Fatalf("runConsumer: %v", err)
	}
	if n != 2 {
		t.Fatalf("wrote %d frames, want 2", n)
	}
	if out.String() != "abcd" {
		t.Fatalf("output = %q, want %q", out.String(), "abcd")
	}

	hr := bytes.NewReader(handshake.Bytes())
	role, err := hr.ReadByte()
	if err != nil || role != roleConsumer {
		t.Fatalf("role byte = %q err=%v, want %q", role, err, roleConsumer)
	}
	id, err := wire.ReadID(hr)
	if err != nil || id != "s1" {
		t.Fatalf("stream id = %q err=%v, want %q", id, err, "s1")
	}
}
