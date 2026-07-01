// Package transcription consumes an external Server-Sent-Events transcription
// stream for an appointment, reassembles the chunks in sequence order, and stores
// the resulting note.
//
// Clinical-safety policy: a note is only "complete" when every chunk in
// [0, finalSeq] is present. A missing middle chunk can invert meaning (dropping
// "not" from "does not report chest pain"), so the assembler never silently
// finalizes over a gap — it produces an explicitly incomplete result instead.
package transcription

import "strings"

// Chunk is one transcription event. Sequence starts at 0 and increments; IsFinal
// marks the last chunk for the appointment.
type Chunk struct {
	AppointmentID string `json:"appointmentId"`
	Sequence      int    `json:"sequence"`
	Text          string `json:"text"`
	IsFinal       bool   `json:"isFinal"`
}

// Assembler reassembles one appointment's transcription chunks. It is not safe
// for concurrent use; a single stream is consumed by a single goroutine.
type Assembler struct {
	chunks    map[int]string
	maxSeq    int
	finalSeq  int
	finalSeen bool
	conflict  bool
}

// NewAssembler returns an empty Assembler.
func NewAssembler() *Assembler {
	return &Assembler{chunks: make(map[int]string)}
}

// Add records a chunk. Duplicates with identical text are ignored; a duplicate
// with different text, or a chunk beyond the final one, marks the stream as
// conflicting (which forces an incomplete result).
func (a *Assembler) Add(c Chunk) {
	if existing, ok := a.chunks[c.Sequence]; ok {
		if existing != c.Text {
			a.conflict = true
		}
		return
	}
	a.chunks[c.Sequence] = c.Text
	if c.Sequence > a.maxSeq {
		a.maxSeq = c.Sequence
	}
	if c.IsFinal {
		a.finalSeen = true
		a.finalSeq = c.Sequence
	}
	// The final chunk must be the highest-numbered one.
	if a.finalSeen && a.maxSeq > a.finalSeq {
		a.conflict = true
	}
}

// IsComplete reports whether every chunk up to the final one has arrived (and no
// conflict occurred), so a consumer can stop reading early.
func (a *Assembler) IsComplete() bool {
	if !a.finalSeen || a.conflict {
		return false
	}
	for i := 0; i <= a.finalSeq; i++ {
		if _, ok := a.chunks[i]; !ok {
			return false
		}
	}
	return true
}

// HadConflict reports whether a contradictory chunk was observed.
func (a *Assembler) HadConflict() bool { return a.conflict }

// Result assembles the ordered text and reports completeness. The note is
// complete only when the final chunk was seen, no conflict occurred, and there
// are no gaps up to the final sequence; otherwise missing lists the absent
// sequence numbers (up to the final chunk, or the highest seen if no final).
func (a *Assembler) Result() (text string, complete bool, missing []int) {
	upTo := a.maxSeq
	if a.finalSeen {
		upTo = a.finalSeq
	}
	var b strings.Builder
	for i := 0; i <= upTo; i++ {
		if t, ok := a.chunks[i]; ok {
			b.WriteString(t)
		} else {
			missing = append(missing, i)
		}
	}
	complete = a.finalSeen && !a.conflict && len(missing) == 0
	return b.String(), complete, missing
}
