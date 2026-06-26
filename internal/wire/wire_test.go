package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"testing/quick"
)

func TestWriteReadRoundTrip(t *testing.T) {
	cases := map[string][]byte{
		"single byte":     []byte("a"),
		"ascii line":      []byte("hello world"),
		"crlf terminator": []byte("line with \r\n inside"),
		"max size":        bytes.Repeat([]byte("x"), MaxFrameSize),
		"binary-ish":      {0x00, 0x01, 0x02, 0xff, 0x0a, 0x0d},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFrame(&buf, payload); err != nil {
				t.Fatalf("WriteFrame(%d bytes): %v", len(payload), err)
			}
			got, err := ReadFrame(&buf)
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Errorf("round-trip mismatch: got %d bytes, want %d", len(got), len(payload))
			}
		})
	}
}

func TestWriteFrameRejectsBadSizes(t *testing.T) {
	if err := WriteFrame(io.Discard, nil); !errors.Is(err, ErrEmptyFrame) {
		t.Errorf("empty payload: got %v, want ErrEmptyFrame", err)
	}
	oversize := make([]byte, MaxFrameSize+1)
	if err := WriteFrame(io.Discard, oversize); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("oversize payload: got %v, want ErrFrameTooLarge", err)
	}
}

func TestReadFrameRejectsZeroLength(t *testing.T) {
	var zero [HeaderSize]byte // length prefix == 0
	if _, err := ReadFrame(bytes.NewReader(zero[:])); !errors.Is(err, ErrEmptyFrame) {
		t.Errorf("zero length: got %v, want ErrEmptyFrame", err)
	}
}

func TestReadFrameRejectsOversizeLength(t *testing.T) {
	var hdr [HeaderSize]byte
	binary.BigEndian.PutUint32(hdr[:], MaxFrameSize+1)
	// Only the header is provided: a correct implementation must reject on the
	// length check before attempting to read (or allocate) the payload.
	if _, err := ReadFrame(bytes.NewReader(hdr[:])); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("oversize length: got %v, want ErrFrameTooLarge", err)
	}
}

func TestReadFrameTruncatedPayload(t *testing.T) {
	var hdr [HeaderSize]byte
	binary.BigEndian.PutUint32(hdr[:], 10)
	r := bytes.NewReader(append(hdr[:], []byte("abcd")...)) // promises 10, supplies 4
	if _, err := ReadFrame(r); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("truncated payload: got %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadFrameCleanEOF(t *testing.T) {
	if _, err := ReadFrame(bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Errorf("empty reader: got %v, want io.EOF", err)
	}
}

func TestQuickRoundTrip(t *testing.T) {
	roundTrips := func(payload []byte) bool {
		if len(payload) == 0 || len(payload) > MaxFrameSize {
			return true // outside the protocol's contract range; skip
		}
		var buf bytes.Buffer
		if err := WriteFrame(&buf, payload); err != nil {
			return false
		}
		got, err := ReadFrame(&buf)
		return err == nil && bytes.Equal(got, payload)
	}
	if err := quick.Check(roundTrips, &quick.Config{MaxCount: 500}); err != nil {
		t.Error(err)
	}
}
