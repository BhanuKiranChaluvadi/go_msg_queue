// Package wire implements the length-prefixed framing protocol used between the
// reader, broker, and writer. Each frame is a 4-byte big-endian uint32 length
// prefix followed by exactly that many raw payload bytes. Payloads are opaque:
// the protocol never inspects, converts, or modifies them, which is what makes a
// byte-identical file copy possible.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// HeaderSize is the size, in bytes, of the length prefix that precedes every
// payload.
const HeaderSize = 4

// MaxFrameSize is the largest payload, in bytes, permitted in a single frame.
// The length prefix is validated against this bound before any payload buffer is
// allocated, so a malformed length can never trigger a huge allocation.
const MaxFrameSize = 65535

// ErrEmptyFrame indicates a frame whose declared length is zero. Zero-length
// frames are reserved and rejected to keep length validation unambiguous.
var ErrEmptyFrame = errors.New("wire: zero-length frame")

// ErrFrameTooLarge indicates a frame whose declared length exceeds MaxFrameSize.
var ErrFrameTooLarge = errors.New("wire: frame exceeds MaxFrameSize")

// WriteFrame writes a single length-prefixed frame to w. The payload is opaque
// and must be in the range 1..MaxFrameSize bytes; otherwise it returns
// ErrEmptyFrame or ErrFrameTooLarge without writing anything.
func WriteFrame(w io.Writer, payload []byte) error {
	n := len(payload)
	switch {
	case n == 0:
		return ErrEmptyFrame
	case n > MaxFrameSize:
		return fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, n, MaxFrameSize)
	}

	var hdr [HeaderSize]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(n))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return nil
}

// ReadFrame reads a single length-prefixed frame from r and returns its payload
// as a freshly allocated slice. It returns io.EOF if the reader is at a clean
// frame boundary with no more data, and io.ErrUnexpectedEOF if a frame is
// truncated mid-way. The length prefix is validated before allocation.
func ReadFrame(r io.Reader) ([]byte, error) {
	var hdr [HeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}

	n := binary.BigEndian.Uint32(hdr[:])
	switch {
	case n == 0:
		return nil, ErrEmptyFrame
	case n > MaxFrameSize:
		return nil, fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, n, MaxFrameSize)
	}

	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}
