# Implementation Plan: `filequeue` Stdlib Messaging System

> Companion to [specifications.md](specifications.md). Plan-mode output — no
> implementation code is written until the human approves and says "start T1".

## Overview

Rebuild the file → queue → file pipeline around a length-prefixed binary frame
protocol so the output is byte-identical to the input. Three processes
(`reader`, `server`, `writer`) connected by raw TCP, stdlib only. The existing
text-protocol implementation is replaced because it cannot preserve exact bytes.

## Current-State Findings (verified in plan mode)

| Finding | Evidence | Impact on plan |
|---|---|---|
| **Project does not compile** | `go build ./...` → *package filequeue/internal/queue is not in std* | `internal/queue` is a **new** package (T2), not a rewrite. |
| **`internal/queue` is missing** | `file_search internal/**/*.go` → no files | Foundation must be built first. |
| **Duplicate `package main`** in writer | [cmd/writer/main.go](cmd/writer/main.go) lines 1–2 | Fixed as part of T5 rewrite. |
| **Text protocol loses fidelity** | `PUSH`/`POP` + `bufio.Scanner` strips terminators | Whole protocol replaced (spec §4). |
| **Makefile & go.mod already correct** | [Makefile](Makefile), [go.mod](go.mod) | No changes needed (T7 verify only). |

## Architecture Decisions (from spec)

- **Opaque byte-chunk framing** (`uint32` big-endian length ‖ payload), not lines —
  guarantees byte-perfect copy.
- **Bounded FIFO** (buffered channel, cap N) → flat memory + TCP-native backpressure.
- **Single stream in core**; `StreamID` routing is a documented additive extension.
- **`[]byte` end-to-end**; broker never interprets payload.

## Dependency Graph

```
internal/wire  (frame encode/decode)         ← foundation, no deps
      │
      ├── internal/queue (bounded FIFO)       ← independent of wire
      │
      ├── cmd/server  (needs wire + queue)
      ├── cmd/reader  (needs wire)
      └── cmd/writer  (needs wire)
                  │
                  └── test/ integration → Makefile verify
```

## Task List

### Phase 1: Foundation

#### Task 1: `internal/wire` frame codec
**Description:** Define the wire format and its encode/decode primitives — the
contract every other component depends on.
**Acceptance criteria:**
- [ ] `MaxFrameSize = 65535`; `WriteFrame(io.Writer, []byte) error`; `ReadFrame(io.Reader) ([]byte, error)`.
- [ ] Rejects `len == 0` and `len > MaxFrameSize` without allocating; uses `io.ReadFull` + `encoding/binary`.
**Verification:**
- [ ] `go test ./internal/wire` — table-driven + `testing/quick` round-trips green.
**Dependencies:** None.
**Files:** `internal/wire/wire.go`, `internal/wire/wire_test.go`.
**Scope:** S

#### Task 2: `internal/queue` bounded FIFO (new package)
**Description:** Create the missing broker queue: a bounded, blocking, closeable
FIFO of `[]byte` frames.
**Acceptance criteria:**
- [ ] `New(capacity int)`; `Push([]byte)` blocks when full; `Pop() ([]byte, bool)` blocks when empty, returns `ok=false` once closed **and** drained.
- [ ] `Close()` idempotent; preserves FIFO order.
**Verification:**
- [ ] `go test -race ./internal/queue` — order, blocking, close-drain.
**Dependencies:** None.
**Files:** `internal/queue/queue.go`, `internal/queue/queue_test.go`.
**Scope:** S

### Checkpoint: Foundation
- [ ] `go test -race ./internal/...` passes.
- [ ] `go build ./internal/...` clean.

### Phase 2: Processes (vertical slice — end-to-end copy)

#### Task 3: `cmd/server` broker rewrite
**Description:** Replace the text protocol with framed transport: accept loop,
role-on-connect (producer/consumer), `ReadFrame`→`Push`, `Pop`→`WriteFrame`,
graceful drain on `SIGTERM`/`SIGINT`, per-conn `recover`, socket deadlines.
**Acceptance criteria:**
- [ ] Producer frames enqueue; consumer dequeues in order; closed+drained ends consumer cleanly.
- [ ] `SIGTERM` stops accepts, drains FIFO, exits; one bad conn never kills the accept loop.
**Verification:**
- [ ] In-process round-trip test (broker on `:0`) passes; `go vet ./cmd/server`.
**Dependencies:** T1, T2.
**Files:** `cmd/server/main.go`.
**Scope:** M

#### Task 4: `cmd/reader` producer rewrite
**Description:** Stream the input file in 32 KiB chunks, frame each, half-close on EOF.
**Acceptance criteria:**
- [ ] Reads via `bufio.Reader`, `WriteFrame` per chunk, `CloseWrite` on EOF, write deadlines; flat memory.
**Verification:**
- [ ] Participates in T6 round-trip; `go vet ./cmd/reader`.
**Dependencies:** T1.
**Files:** `cmd/reader/main.go`.
**Scope:** S

#### Task 5: `cmd/writer` consumer rewrite
**Description:** Read frames, write raw payloads to the output file, flush+`Sync` on
close. Also fixes the duplicate `package main` clause.
**Acceptance criteria:**
- [ ] `ReadFrame` loop into `bufio.Writer`; on closed stream → `Flush` + `file.Sync` + close; read deadlines.
- [ ] No duplicate `package` clause; `go vet` clean.
**Verification:**
- [ ] Participates in T6 round-trip; `go build ./cmd/writer`.
**Dependencies:** T1.
**Files:** `cmd/writer/main.go`.
**Scope:** S

### Checkpoint: Core Features
- [ ] `go build ./...` clean (the currently-failing build now passes).
- [ ] Three-process manual run copies a small file correctly.

### Phase 3: Verification

#### Task 6: Integration fidelity tests
**Description:** Prove byte-perfect copy across edge cases and race-freedom.
**Acceptance criteria:**
- [ ] `sha256` equal for: empty, no-trailing-`\n`, `\r\n`, line > 64 KiB, binary-ish bytes.
- [ ] Graceful-shutdown test; `-race` clean.
**Verification:**
- [ ] `go test -race ./test/...`.
**Dependencies:** T3, T4, T5.
**Files:** `test/roundtrip_test.go`, `test/testdata/*`.
**Scope:** M

#### Task 7: Build/run gate
**Description:** Confirm the Make targets and dependency hygiene.
**Acceptance criteria:**
- [ ] `make build`, `make test` green; `make run` prints "output matches input".
- [ ] `go.mod` has zero external requires.
**Verification:**
- [ ] Run the three Make targets.
**Dependencies:** T6.
**Files:** none.
**Scope:** XS

### Checkpoint: Complete
- [ ] All spec §13 success criteria met.
- [ ] Ready for review.

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Reader/writer role disambiguation on a shared broker | Med | Role byte/flag on connect; keep conn state isolated (future StreamID). |
| EOF vs. transient-empty confusion at writer | Med | Producer half-closes; queue closes only after producer EOF **and** drain. |
| Partial TCP reads | High | `io.ReadFull` everywhere; never trust a single `Read`. |
| Hidden coupling to old `string` queue API | Low | `internal/queue` is new; no callers besides the rewritten server. |
| `make run` race (server not ready) | Low | Makefile already sleeps; add dial-retry in workers if flaky. |

## Parallelization

- **Sequential:** T1/T2 → T3 (server needs both).
- **Parallel-safe after T1:** T4 and T5.
- **After processes:** T6 → T7.

## Open Questions

1. Demonstrate reconnection (spec §11.1) in the core, or discuss live? *(Recommend: discuss live.)*
2. Keep 32 KiB chunk default? *(Recommend: yes.)*
