package main

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"filequeue/internal/wire"
)

func TestCopyFileToConnFramesRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		data  []byte
		chunk int
	}{
		{"empty", nil, 8},
		{"smaller than chunk", []byte("hello"), 8},
		{"exact chunk multiple", []byte("abcdefgh"), 4},
		{"partial final chunk", []byte("abcdefghij"), 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var conn bytes.Buffer
			frames, err := copyFileToConn(bytes.NewReader(tc.data), &conn, tc.chunk)
			if err != nil {
				t.Fatalf("copyFileToConn: %v", err)
			}

			r := bytes.NewReader(conn.Bytes())
			var got []byte
			read := 0
			for {
				p, err := wire.ReadFrame(r)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("ReadFrame: %v", err)
				}
				got = append(got, p...)
				read++
			}
			if read != frames {
				t.Fatalf("read %d frames, copyFileToConn reported %d", read, frames)
			}
			if !bytes.Equal(got, tc.data) {
				t.Fatalf("reassembled %q, want %q", got, tc.data)
			}
		})
	}
}

// errWriter fails on the first Write to prove copyFileToConn surfaces write
// errors instead of swallowing them.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }

func TestCopyFileToConnReturnsWriteError(t *testing.T) {
	_, err := copyFileToConn(bytes.NewReader([]byte("data")), errWriter{}, 2)
	if err == nil {
		t.Fatal("expected the write error to propagate")
	}
}

func TestRunProducerWritesHandshakeThenFrames(t *testing.T) {
	var conn bytes.Buffer
	frames, err := runProducer(&conn, bytes.NewReader([]byte("abcd")), "s1", 2)
	if err != nil {
		t.Fatalf("runProducer: %v", err)
	}
	if frames != 2 {
		t.Fatalf("sent %d frames, want 2", frames)
	}

	r := bytes.NewReader(conn.Bytes())
	role, err := r.ReadByte()
	if err != nil || role != roleProducer {
		t.Fatalf("role byte = %q err=%v, want %q", role, err, roleProducer)
	}
	id, err := wire.ReadID(r)
	if err != nil || id != "s1" {
		t.Fatalf("stream id = %q err=%v, want %q", id, err, "s1")
	}
	var got []byte
	for {
		p, err := wire.ReadFrame(r)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		got = append(got, p...)
	}
	if string(got) != "abcd" {
		t.Fatalf("payload = %q, want %q", got, "abcd")
	}
}
