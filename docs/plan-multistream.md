# Implementation Plan: Multi-Stream Broker (N producers / N consumers)

> Scope note: this plan covers the multi-stream extension only. The single-stream
> core plan lives in [plan.md](plan.md) and is unchanged.

## Overview
Today the broker is a single anonymous FIFO: every producer pushes into one queue,
every consumer pops from it, with no identity or routing. This plan adds **stream
addressing** so each file moves through its own logical channel: a producer tags
its connection with a `StreamID`, and a consumer subscribes to the same `StreamID`
and receives exactly that file — byte-perfect, independently, concurrently. The
single-stream CLI keeps working unchanged via a default stream.

## Architecture Decisions
- **Connection-level addressing, not per-frame.** Each TCP connection is dedicated
  to one role and one file, so the `StreamID` is sent **once, in the handshake**,
  right after the role byte. The `wire` frame format is untouched, so byte-perfect
  framing is preserved with no per-frame overhead. Per-frame multiplexing
  (HTTP/2-style) is explicitly out of scope.
- **Reuse `wire` for the handshake.** The `StreamID` is sent as one ordinary
  length-prefixed frame, reusing `WriteFrame`/`ReadFrame` instead of inventing a
  second encoding.
- **Per-stream queue registry.** The broker holds `map[StreamID]*queue.Queue`
  behind a mutex. Producer/consumer for stream X get-or-create queue X. This is
  what pairs a writer to the right reader.
- **Per-stream, reference-counted close.** A stream's queue closes when *its*
  producer finishes — never the global set. This fixes the premature-global-`Close`
  truncation bug. The registry entry is retained while it holds undrained buffered
  frames, and deleted once closed **and** drained, so the map does not leak.
- **Single consumer per stream (decided).** A second consumer on a live stream is
  rejected, not load-balanced. This matches the file-copy goal and keeps the design
  simple.
- **Admission control.** A `-max-streams` cap bounds total memory
  (`maxStreams x capacity x MaxFrameSize`) and prevents a stream-creation DoS.
- **Backward compatible.** `reader`/`writer` default `-stream` to `"default"`, so
  existing single-stream behavior and all current tests are preserved.

### Resolved open questions
- **Work-queue / broadcast modes: out of scope.** Only single-consumer-per-stream
  copy semantics are implemented. Load-balanced work-queue and fan-out/broadcast
  (every consumer gets a full copy) are each a separate future feature, not part of
  this plan.
- **Stream-not-found / no-producer (best practice): bounded attach timeout.** A
  consumer that subscribes to a stream with no producer waits up to
  `-attach-timeout` (default 10s) for the stream to become active, then exits
  non-zero with a clear message. Fail-fast and observable beats a hung client or a
  leaked goroutine that blocks until shutdown.
- **StreamID namespace (best practice): validated token.** IDs are 1-64 bytes from
  the charset `[A-Za-z0-9._-]`; anything else is rejected at the handshake. No
  per-stream authentication in this change — authentication belongs to the TLS/mTLS
  production extension, not to routing.

## Dependency Graph
```
wire StreamID handshake (T1)
        |
   stream registry  (T2)   <- riskiest: lifecycle race. Build & unit-test early.
        |
   broker routing   (T3)
        |
   reader/writer -stream + -attach-timeout flags (T4)
        |
   multi-stream integration tests (T5)
        |
   enforcement + cleanup hardening (T6)
        |
   spec/diagram update (T7)
```

## Task List

### Phase 1: Foundation

#### Task 1: Stream-ID handshake codec
**Description:** Add thin helpers to send/receive a `StreamID` as a single `wire`
frame after the role byte, with token validation (1-64 bytes, `[A-Za-z0-9._-]`).
**Acceptance criteria:**
- [ ] `WriteID`/`ReadID` round-trip a valid ID; reject empty, over-long, and
      out-of-charset IDs.
- [ ] Frame format and existing `wire` tests are unchanged.
**Verification:** `go test -race ./internal/wire/`
**Dependencies:** None
**Files:** `internal/wire/wire.go`, `internal/wire/wire_test.go`
**Scope:** S

#### Task 2: Stream registry (get-or-create + ref-counted lifecycle)
**Description:** A concurrency-safe registry mapping `StreamID -> *queue.Queue`,
tracking active producer/consumer references, retaining closed-but-undrained
queues, and deleting entries once closed and drained. Built and tested in isolation
first because the lifecycle race is the riskiest part.
**Acceptance criteria:**
- [ ] Concurrent get-or-create for the same ID returns the same queue.
- [ ] Producer-close marks the stream closed but a late consumer still drains
      buffered frames; the entry is removed only after drain.
- [ ] `maxStreams` rejects creation beyond the cap.
**Verification:** `go test -race ./internal/broker/` (new package)
**Dependencies:** T1
**Files:** `internal/broker/registry.go`, `internal/broker/registry_test.go`
**Scope:** M

### Checkpoint: Foundation (after T1-T2)
- [ ] `go test -race ./...` green; registry lifecycle proven under `-race` before
      any wiring.

### Phase 2: Routing slice

#### Task 3: Broker uses registry + handshake
**Description:** `handleProducer`/`handleConsumer` read the `StreamID`, route to the
per-stream queue, close only their own stream on producer EOF, and reject a second
consumer on a live stream.
**Acceptance criteria:**
- [ ] Producer EOF closes only its stream; other streams keep running.
- [ ] Second consumer on an active stream is rejected with a clear log; the first is
      unaffected.
- [ ] Idle-deadline and graceful-shutdown behavior preserved.
**Verification:** `go test -race ./test/` (existing single-stream tests still pass
via the default stream)
**Dependencies:** T2
**Files:** `cmd/server/main.go`
**Scope:** M

#### Task 4: `-stream` and `-attach-timeout` flags on reader/writer
**Description:** Add `-stream` (default `"default"`) to both workers and
`-attach-timeout` (default 10s) to the writer; send the ID in the handshake.
**Acceptance criteria:**
- [ ] Omitting `-stream` reproduces today's exact single-stream behavior.
- [ ] `reader -stream a` and `writer -stream a` pair up; `writer -stream b` does not
      receive stream `a`'s data.
- [ ] A writer subscribing to a stream with no producer exits non-zero after
      `-attach-timeout`.
**Verification:** existing `TestRoundTripFidelity` unchanged + a two-stream manual run.
**Dependencies:** T3
**Files:** `cmd/reader/main.go`, `cmd/writer/main.go`
**Scope:** S

### Checkpoint: Core (after T3-T4)
- [ ] Single-stream copy still byte-perfect; two named streams stay isolated
      end-to-end.

### Phase 3: Behavior, hardening, docs

#### Task 5: Multi-stream integration tests
**Description:** End-to-end tests for 2 producers + 2 consumers on distinct streams
producing two byte-perfect outputs with no interleaving and independent completion;
plus negatives (duplicate consumer rejected, `maxStreams` enforced, attach timeout).
**Acceptance criteria:**
- [ ] Two files copied concurrently; each output `sha256`-matches its input.
- [ ] One stream finishing does not truncate the other.
- [ ] Rejection and attach-timeout paths covered.
**Verification:** `go test -race ./test/`
**Dependencies:** T4
**Files:** `test/roundtrip_test.go`
**Scope:** M

#### Task 6: Lifecycle hardening (leak + cleanup)
**Description:** Confirm registry entries and goroutines are released after each
stream completes; enforce `maxStreams` and single-consumer cleanly under load.
**Acceptance criteria:**
- [ ] After N sequential streams, registry size returns to 0 and goroutine count is
      stable (no leak).
- [ ] Cap and rejection behavior verified under concurrent connects.
**Verification:** `go test -race ./internal/broker/ ./test/`
**Dependencies:** T5
**Files:** `internal/broker/registry.go`, tests
**Scope:** S

#### Task 7: Spec + diagram update
**Description:** Promote spec section 11.3 from "deferred extension" to implemented;
document the handshake, registry, lifecycle, attach timeout, ID validation, and
limits; update the architecture diagrams.
**Acceptance criteria:**
- [ ] Spec reflects the addressed protocol and its boundaries (no per-frame mux, no
      fan-out, single consumer per stream).
**Verification:** doc review.
**Dependencies:** T6
**Files:** `docs/specifications.md`
**Scope:** XS

### Checkpoint: Complete (after T5-T7)
- [ ] All acceptance criteria met; `go test -race ./...` green; spec current.

## Risks and Mitigations
| Risk | Impact | Mitigation |
|------|--------|------------|
| Registry lifecycle race: producer closes/removes a stream before its consumer attaches, so the consumer blocks or misses buffered data | High | Build T2 first, test under `-race`; retain closed-but-undrained queues; delete only after drain |
| Unbounded stream map -> memory/DoS | Med | `-max-streams` admission cap; delete entries on completion (T6) |
| Consumer subscribes to a stream whose producer never connects | Med | Bounded `-attach-timeout`, then exit non-zero (resolved best practice) |
| Breaking the existing single-stream contract | Low | Default `-stream` + keep all current tests passing at every step (T3, T4) |

## Out of Scope (future, separate features)
- Work-queue mode (load-balanced competing consumers).
- Broadcast / fan-out (every consumer receives a full copy).
- Per-frame stream multiplexing over a single connection.
- Per-stream authentication / ownership (covered by the TLS/mTLS extension).
