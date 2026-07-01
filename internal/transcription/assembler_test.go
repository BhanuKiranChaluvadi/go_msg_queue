package transcription

import (
	"reflect"
	"testing"
)

// add feeds a sequence of chunks into a fresh assembler.
func assemble(chunks ...Chunk) (string, bool, []int) {
	a := NewAssembler()
	for _, c := range chunks {
		a.Add(c)
	}
	return a.Result()
}

func ch(seq int, text string, final bool) Chunk {
	return Chunk{AppointmentID: "appt-1", Sequence: seq, Text: text, IsFinal: final}
}

func TestAssembler_InOrderComplete(t *testing.T) {
	text, complete, missing := assemble(ch(0, "a", false), ch(1, "b", false), ch(2, "c", true))
	if !complete || text != "abc" || len(missing) != 0 {
		t.Errorf("got (%q, %v, %v), want (abc, true, [])", text, complete, missing)
	}
}

func TestAssembler_OutOfOrderComplete(t *testing.T) {
	// Final (seq 2) arrives first, then the earlier chunks.
	text, complete, missing := assemble(ch(2, "c", true), ch(0, "a", false), ch(1, "b", false))
	if !complete || text != "abc" || len(missing) != 0 {
		t.Errorf("got (%q, %v, %v), want (abc, true, [])", text, complete, missing)
	}
}

func TestAssembler_DuplicateIdempotent(t *testing.T) {
	text, complete, _ := assemble(ch(0, "a", false), ch(0, "a", false), ch(1, "b", true))
	if !complete || text != "ab" {
		t.Errorf("got (%q, %v), want (ab, true)", text, complete)
	}
}

func TestAssembler_GapFilledBeforeFinal(t *testing.T) {
	// 0, then final at 2 (gap at 1), then 1 fills the gap.
	a := NewAssembler()
	a.Add(ch(0, "a", false))
	a.Add(ch(2, "c", true))
	if a.IsComplete() {
		t.Fatal("should not be complete with a gap at 1")
	}
	a.Add(ch(1, "b", false))
	if !a.IsComplete() {
		t.Fatal("should be complete once the gap is filled")
	}
	text, complete, missing := a.Result()
	if !complete || text != "abc" || len(missing) != 0 {
		t.Errorf("got (%q, %v, %v), want (abc, true, [])", text, complete, missing)
	}
}

func TestAssembler_GapUnfilledAtFinalIncomplete(t *testing.T) {
	// 0, 1, final at 3 — sequence 2 never arrives.
	text, complete, missing := assemble(ch(0, "a", false), ch(1, "b", false), ch(3, "d", true))
	if complete {
		t.Error("should be incomplete with a missing middle chunk")
	}
	if !reflect.DeepEqual(missing, []int{2}) {
		t.Errorf("missing = %v, want [2]", missing)
	}
	// The available text is still assembled in order (gap omitted).
	if text != "abd" {
		t.Errorf("text = %q, want abd", text)
	}
}

func TestAssembler_ConflictingDuplicateIncomplete(t *testing.T) {
	a := NewAssembler()
	a.Add(ch(0, "a", false))
	a.Add(ch(1, "not", false))
	a.Add(ch(1, "now", false)) // contradicts seq 1
	a.Add(ch(2, "c", true))

	if !a.HadConflict() {
		t.Error("expected a conflict to be detected")
	}
	_, complete, _ := a.Result()
	if complete {
		t.Error("a conflicting stream must never be reported complete")
	}
}

func TestAssembler_FinalNotLastIsConflict(t *testing.T) {
	// A chunk arrives beyond the declared final -> protocol violation.
	a := NewAssembler()
	a.Add(ch(0, "a", false))
	a.Add(ch(1, "b", true)) // final at 1
	a.Add(ch(2, "c", false))
	if !a.HadConflict() {
		t.Error("a chunk beyond the final one should be a conflict")
	}
	if _, complete, _ := a.Result(); complete {
		t.Error("must not be complete when final is not the last chunk")
	}
}

func TestAssembler_NoFinalIsIncomplete(t *testing.T) {
	// Contiguous 0,1 but the stream ended without a final chunk.
	text, complete, missing := assemble(ch(0, "a", false), ch(1, "b", false))
	if complete {
		t.Error("without a final chunk the note is not complete")
	}
	if text != "ab" || len(missing) != 0 {
		t.Errorf("got (%q, missing %v), want (ab, [])", text, missing)
	}
}
