# Implementation Plan: `medconnect` — Healthcare Appointment Service

> Companion to [specifications-healthcare.md](specifications-healthcare.md) and the
> brief in [Question.md](Question.md). This document turns the spec into an
> **ordered, vertically-sliced task list**. We build **one feature at a time**,
> test it (`go test -race`), reach a checkpoint, and only then proceed.
>
> **How to read this:** Phase 0 is non-negotiable foundation. Phases 1–8 each map
> to one feature area of the brief. Every task is sized **S** (1–2 files) or **M**
> (3–5 files); nothing is larger. Each task has acceptance criteria, a verification
> command, dependencies, and the files it touches.

---

## Overview

A stdlib-only Go HTTP service for hospital tenants: patients book appointments and
subscribe to webhooks; doctors publish timeslots, dictate/store notes (streamed via
SSE), prescribe, and diagnose; pharmacists dispatch prescriptions. An append-only
event log underpins historical overview, audit, and analytics. Storage is
in-memory behind interfaces so a Postgres/CockroachDB adapter can drop in later.

We deliver in **vertical slices**: each task wires domain → store → service →
HTTP handler → test for one capability, leaving the system runnable and green
after every task.

---

## Architecture Decisions

These lock the shape of the code before we start. Rationale is in the spec; the
decisions that directly drive the task breakdown are:

1. **Hexagonal layering.** `domain` (pure types + rules) ← `store` ports +
   in-memory adapters ← feature `services` ← `api` handlers. Handlers are thin;
   rules live in services; I/O is behind interfaces. *Consequence:* every feature
   slice touches these layers in the same order.

2. **`net/http.ServeMux` with method+path patterns (Go 1.22+).** e.g.
   `mux.HandleFunc("POST /v1/timeslots", …)` and `{id}` wildcards. **No router
   dependency.** *Consequence:* routing is a foundation task; features just add
   routes.

3. **Event log is foundational, not a late add-on.** Services publish an immutable
   `Event` (with `actorId` + `timestamp`) on every mutation from day one, via an
   in-process `Publisher` that (a) appends to an append-only store and (b) fans out
   to subscribers. *Consequence:* Audit (F6), History (F4), Analytics (F7), and
   Webhooks (F3) are **consumers** added later with **zero rework** to the
   producers. This is the single most important ordering decision.

4. **Injectable `Clock` and `IDGen`.** All time and id generation flow through
   interfaces (`crypto/rand` hex ids, `time.Now` clock). *Consequence:* expiry,
   point-in-time overview, and ordering are **deterministically testable**.

5. **Tenancy + actor via `context.Context`.** Middleware resolves `X-Tenant-ID` /
   `X-User-ID` → context; every store method is tenant-scoped. *Consequence:*
   isolation is enforced in one place, testable as a cross-cutting concern.

6. **Concurrency: mutex + one critical section per invariant (not actors).** Maps +
   `sync.RWMutex`; booking and dispatch do their check-and-set inside a *single*
   locked section so two racers can never both win. We deliberately reject an
   actor/channel-per-aggregate model: the service must be **stateless** to scale
   (10 M users, multi-region), so the real concurrency authority is the **DB
   transaction**, and the in-memory mutex is a 1:1 stand-in a SQL adapter later
   replaces. An actor model implies single-process *stateful* ownership that
   contradicts stateless scaling. *Consequence:* `go test -race` is a first-class
   gate on every task that has an invariant (Tasks 1.2, 4.2). Full rationale in
   spec §4.7.

7. **Transport per channel:** REST (server) + SSE (client, transcription) +
   outbound webhooks (client). No WebSocket/gRPC. (Full justification in spec §4.)

8. **Transcription assembler: ordered, gap-aware, bounded-wait, explicitly
   incomplete.** Never silently finalize over a missing `sequence` (a dropped
   middle chunk can invert clinical meaning) and never wait forever. A note is
   `complete` only when `0..finalSeq` are contiguous; otherwise, after a bounded
   timeout, it is stored `incomplete` (with missing sequences) and raises a
   `note_incomplete` event instead of `note_added`. *Consequence:* the `Note` type
   carries `status` + `missing` (Task 0.2), and Task 3.2's tests cover the gap
   cases explicitly. Full rationale in spec §5.2.

9. **Process topology: one modular binary by default; the worker split is an
   optional, reversible seam.** The *sustainable* default is a **single `cmd/server`
   binary** that runs the transcription and webhook workers **embedded as
   goroutines**, calling the in-process `Publisher` directly — simplest to run,
   test, and reason about, with atomic deploys and no distributed failure modes.
   The workers live in their own `internal/` packages behind interfaces (DIP), so
   they can *optionally* be deployed as **separate binaries** (`cmd/transcriber`,
   `cmd/notifier`) — the `filequeue` server/reader/writer shape — selected by an
   `-embed-workers=false` flag at the composition root, **with zero logic change**.
   We keep the split as a *seam*, not a default, because splitting only becomes
   more sustainable once state is externalized (CockroachDB) and there is real
   independent-scaling pressure; until then, separate processes just add a network
   contract to version and operate. **Two rules keep the seam sustainable:** (a) the
   internal contract stays **tiny and versioned** (three `/internal/*` endpoints) so
   a split never forces lockstep deploys; (b) **no two bounded contexts share a
   table/schema**, so a future extraction is a lift-and-shift, not a rewrite. In
   production the in-memory store becomes CockroachDB and the in-process publisher
   becomes Kafka/NATS (same seams). See the
   [Process Topology](#process-topology--internal-protocol) section.

**Commands (new module `medconnect`):**

```bash
make build          # go build ./...  (server [+ optional transcriber, notifier])
make test           # go test ./...
make race           # go test -race ./...
make check          # fmt + vet + build + race
make run            # DEFAULT: single binary, workers embedded, on :8080
# --- optional split mode (turn on only when justified) ---
make run-split      # server (-embed-workers=false) + transcriber + notifier
make run-transcriber# transcription worker as its own process (connects to the hub)
make run-notifier   # webhook dispatcher as its own process (connects to the hub)
```

---

## Dependency Graph

```
Phase 0 Foundation  (all inside cmd/server, the hub)
  domain types
     │
  store ports + in-memory adapters ── Clock/IDGen
     │
  tenancy/actor middleware + role guard
     │
  event log store + in-process publisher
     │
  HTTP bootstrap (ServeMux, JSON envelope, error mapping)
     │
  internal protocol contract (hub ⇆ workers: HTTP + SSE)   ← Task 0.7
     │
  ├── Phase 1  Appointments (F1)  ── emits note_added / prescription_added events
  │       │
  │       ├── Phase 2  Webhooks (F3)      [cmd/notifier: consumes hub event stream]
  │       ├── Phase 3  Transcription (F2) [cmd/transcriber: external SSE → notes → hub]
  │       └── Phase 4  Dispatch (F5)      [prescription state machine]
  │
  ├── Phase 5  Historical Overview (F4)   [folds event log]
  ├── Phase 6  Audit Trail (F6)           [view over event log]
  ├── Phase 7  Analytics (F7)             [aggregates event log]
  └── Phase 8  Multi-Tenancy + hardening (F8)
```

**Why this order:** Webhooks (Phase 2) come before Transcription (Phase 3) so the
transcription E2E test can assert the *full* chain (note stored → `note_added` →
`cmd/notifier` webhook POST). Audit/History/Analytics come after the producers
exist because they only *read* the log they've been filling since Phase 1.

---

## Process Topology & Internal Protocol

**Default (sustainable): one modular binary.** `cmd/server` runs the transcription
and webhook workers **embedded as goroutines**, calling the in-process `Publisher`
directly. Simplest to run/test, atomic deploys, no distributed failure modes. The
workers live in their own `internal/` packages behind interfaces, so the boundaries
are already clean — the split below is a *deployment* choice, not a code change.

```
DEFAULT — embedded (make run)
        clients (REST/JSON)
              │
        ┌─────▼─────────────────────────────────────┐
        │  cmd/server  (single binary)              │
        │  REST API + in-memory store + Publisher   │
        │  ├─ transcription worker  (goroutine)     │──► external transcription SSE
        │  └─ webhook dispatcher    (goroutine)     │──► patient webhook URLs
        └───────────────────────────────────────────┘
```

**Optional (turn on only when justified): split into worker binaries.** Selected by
`-embed-workers=false`; the workers become standalone processes that talk to the hub
over the tiny internal contract — the `filequeue` server/reader/writer shape, no
docker. Justified once state is externalized (CockroachDB) and a workload needs
independent scaling.

```
OPTIONAL — split (make run-split)
        clients (REST/JSON)
              │
        ┌─────▼──────────────────────────┐
        │  cmd/server  (the hub)         │
        │  REST API + store + Publisher  │
        │  + internal event stream (SSE) │
        │  + transcription job queue     │
        └──▲───────────────────────┬─────┘
   POST     │ (assembled notes)     │ GET /internal/events (SSE fan-out)
 note back  │                       ▼
        ┌───┴───────────┐   ┌───────────────────┐
        │ cmd/transcriber│   │ cmd/notifier      │──► patient webhook URLs
        │ external SSE → │   │ (webhook dispatch,│
        │ assembler      │   │  retry + HMAC)    │
        └──▲────────────┘    └───────────────────┘
           │ external SSE
   external transcription server
```

**Internal contract (defined in Task 0.7, stdlib only — used in split mode; in
embedded mode the same packages are called in-process):**

| Endpoint (server-side, `/internal/*`) | Consumer | Shape |
|---|---|---|
| `GET /internal/events` | `cmd/notifier` | **SSE** stream of domain events (the in-process publisher, re-emitted over the network) |
| `GET /internal/transcription/jobs` | `cmd/transcriber` | long-poll/SSE of "start transcription" jobs `{appointmentId, streamURL}` |
| `POST /internal/appointments/{id}/notes` | `cmd/transcriber` | posts an assembled note back (`status=complete|incomplete`, `missing`) |

**Two rules that keep the seam sustainable:**
1. **Tiny, versioned contract** — only the three `/internal/*` endpoints above; a
   split must never force lockstep deploys.
2. **No shared table/schema across bounded contexts** — each context owns its data,
   so a future extraction is a lift-and-shift, not a rewrite.

- **Symmetry (reused machinery):** in split mode, `cmd/notifier` consuming the hub's
  `/internal/events` **SSE** mirrors `cmd/transcriber` consuming the **external**
  transcription SSE — same client code. `cmd/transcriber` posting a note back
  mirrors a doctor posting a manual note.
- **Auth between processes:** a shared internal token header for the core; mTLS in
  production (design-only).
- **One switch, no rewrite:** embedded vs. split is chosen only at the composition
  root (`-embed-workers`); worker logic is identical either way (DIP payoff).

---

# Phase 0 — Foundation

> Goal: an empty-but-runnable service with all cross-cutting machinery, so every
> later feature is a thin vertical slice.

## Task 0.1: Module scaffold + server bootstrap + health check

**Description:** Create the `medconnect` module, directory skeleton, Makefile, and
a `cmd/server` that starts an `http.Server` with graceful shutdown and a
`GET /healthz` endpoint.

**Acceptance criteria:**
- [ ] `go build ./...` succeeds; `make run` serves `GET /healthz` → `200 {"status":"ok"}`.
- [ ] `SIGINT`/`SIGTERM` triggers graceful shutdown (server stops accepting, drains).

**Verification:**
- [ ] `make build` and a manual `curl localhost:8080/healthz`.
- [ ] `go vet ./...`, `gofmt -l .` clean.

**Dependencies:** None
**Files:** `go.mod`, `Makefile`, `cmd/server/main.go`, `internal/api/server.go`
**Scope:** M

## Task 0.2: Domain model types

**Description:** Define pure Go types for every entity (Tenant, User+Role,
Timeslot, Appointment, Note, Prescription, Diagnosis, Webhook, Event, Dispatch)
plus status enums and sentinel errors. No behaviour beyond simple methods
(e.g. `Prescription.IsActive(now)`).

**Acceptance criteria:**
- [ ] All entities from spec §3 exist with `TenantID` on each.
- [ ] Status enums (`open|booked`, `active|dispatched|expired`, `complete|incomplete` note status, roles) are typed constants.
- [ ] `Note` carries `status` + `missing []int` (gap-aware assembly, spec §5.2); manual notes are `complete`.
- [ ] Event types include `note_added`, `note_incomplete`, `prescription_added` (+ internal mutation events).
- [ ] `Prescription.IsActive(now)` and `Diagnosis.IsActiveAt(t)` unit-tested.

**Verification:**
- [ ] `go test ./internal/domain` passes.

**Dependencies:** 0.1
**Files:** `internal/domain/types.go`, `internal/domain/status.go`, `internal/domain/domain_test.go`
**Scope:** M

## Task 0.3: Storage ports + in-memory adapters

**Description:** Define `Repository` interfaces per aggregate and a generic
tenant-scoped in-memory store (`map[tenant]map[id]T` + `sync.RWMutex`). Provide
adapters for each entity.

**Acceptance criteria:**
- [ ] Interfaces: `TimeslotRepo`, `AppointmentRepo`, `NoteRepo`, `PrescriptionRepo`, `DiagnosisRepo`, `WebhookRepo`.
- [ ] All methods take `ctx` and are tenant-scoped; cross-tenant reads impossible.
- [ ] Generic store CRUD unit-tested including tenant isolation.

**Verification:**
- [ ] `go test -race ./internal/store/...` passes.

**Dependencies:** 0.2
**Files:** `internal/store/ports.go`, `internal/store/memory/store.go`, `internal/store/memory/store_test.go`
**Scope:** M

## Task 0.4: Tenancy + actor middleware + role guard + Clock/IDGen

**Description:** Middleware that reads `X-Tenant-ID`/`X-User-ID`, looks up role,
and injects `tenantID` + `actor` into context; helpers `TenantFrom(ctx)`,
`ActorFrom(ctx)`; a `RequireRole(role)` guard. Add `Clock` and `IDGen` interfaces
with real + fake implementations.

**Acceptance criteria:**
- [ ] Missing/invalid tenant → `401`; wrong role on a guarded route → `403`.
- [ ] `TenantFrom`/`ActorFrom` return injected values; fakes usable in tests.

**Verification:**
- [ ] `go test ./internal/tenancy` (httptest) passes.

**Dependencies:** 0.1
**Files:** `internal/tenancy/context.go`, `internal/tenancy/middleware.go`, `internal/platform/clock.go`, `internal/platform/id.go`, `internal/tenancy/middleware_test.go`
**Scope:** M

## Task 0.5: Event log store + in-process publisher

**Description:** Append-only `events.Store` (tenant-scoped, ordered, queryable by
time range / entity) and a `Publisher` that appends and fans out to registered
`Subscriber`s synchronously. Services depend on `Publisher`.

**Acceptance criteria:**
- [ ] `Publish(ctx, Event)` appends (immutable) and notifies all subscribers.
- [ ] `Query(ctx, filter)` returns tenant-scoped events ordered by `(timestamp,id)`.
- [ ] A test subscriber receives published events; append order is stable.

**Verification:**
- [ ] `go test -race ./internal/events` passes.

**Dependencies:** 0.3, 0.4
**Files:** `internal/events/event.go`, `internal/events/store.go`, `internal/events/publisher.go`, `internal/events/events_test.go`
**Scope:** M

## Task 0.6: HTTP JSON envelope + error mapping + request logging

**Description:** Shared helpers: `writeJSON`, `writeError` (maps domain sentinel
errors → HTTP codes with `{ "error": { code, message } }`), request-id + `slog`
middleware, and the middleware chain assembly.

**Acceptance criteria:**
- [ ] `ErrNotFound→404`, `ErrConflict→409`, `ErrValidation→400`, `ErrForbidden→403`.
- [ ] Every response is JSON; every request logged with method/path/status/latency/request-id.

**Verification:**
- [ ] `go test ./internal/api` (httptest table of error→status) passes.

**Dependencies:** 0.1, 0.4
**Files:** `internal/api/respond.go`, `internal/api/errors.go`, `internal/api/middleware.go`, `internal/api/respond_test.go`
**Scope:** M

## Task 0.7: Worker seam + optional internal protocol contract

**Description:** Establish the seam that lets the transcription and webhook workers
run **embedded by default** or **split** later, with no logic change. Define a
`Worker` interface and a composition switch (`-embed-workers`, default `true`) at
the root. Also define the *tiny* internal contract used **only in split mode** — the
`/internal/events` SSE fan-out (re-emits the in-process publisher over the network),
the `/internal/transcription/jobs` feed, and the
`POST /internal/appointments/{id}/notes` write-back — in a reusable
`internal/protocol` package (shared DTOs + one SSE client reused by both workers)
behind an internal auth-token middleware. No feature logic yet — just the seam.

**Acceptance criteria:**
- [ ] `-embed-workers=true` (default) wires workers as in-process goroutines calling the `Publisher` directly; `=false` wires the `/internal/*` transport — **same worker packages** either way.
- [ ] `internal/protocol` defines the event + job DTOs and the shared SSE client (reused by Phases 2 & 3).
- [ ] `GET /internal/events` streams published events as SSE to a connected client (httptest); internal endpoints require the shared token → `401` without it.
- [ ] Contract kept to exactly the three `/internal/*` endpoints (tiny + versioned); no bounded context shares another's store.

**Verification:**
- [ ] `go test -race ./internal/protocol` (publish → SSE client receives) passes.

**Dependencies:** 0.5, 0.6
**Files:** `internal/protocol/dto.go`, `internal/protocol/sse_client.go`, `internal/api/internal_stream.go`, `internal/protocol/protocol_test.go`
**Scope:** M

### ✅ Checkpoint A — Foundation
- [ ] `make check` green (fmt, vet, build, `-race`).
- [ ] `cmd/server` runs as a **single binary with workers embedded** (default);
      `/healthz` works; tenant/role middleware enforced; publisher + store tested.
- [ ] Split seam verified: `-embed-workers=false` exposes token-guarded
      `/internal/events` SSE; `internal/protocol` client reused-ready for both workers.
- [ ] **Human review before Phase 1.**

---

# Phase 1 — Appointments Management (Feature 1)

> Vertical slices; each emits events where the brief implies a change.

## Task 1.1: Timeslots — register (doctor) + list available (patient)

**Description:** Doctor registers a timeslot; patient lists a doctor's open
timeslots.

**Acceptance criteria:**
- [ ] `POST /v1/timeslots` (doctor-only) creates an `open` timeslot; validates `start<end`, no overlap for that doctor.
- [ ] `GET /v1/doctors/{id}/timeslots?status=open` returns only open, future slots.

**Verification:**
- [ ] `go test -race ./internal/appointments -run Timeslot` + httptest handler test.

**Dependencies:** Checkpoint A
**Files:** `internal/appointments/service.go`, `internal/appointments/timeslots.go`, `internal/api/timeslots.go`, `internal/appointments/timeslots_test.go`
**Scope:** M

## Task 1.2: Booking — book appointment (patient) with invariants

**Description:** Patient books an open timeslot. Enforce **≤1 appointment per
patient–doctor pair** and **no double-booking**, concurrency-safe.

**Acceptance criteria:**
- [ ] `POST /v1/appointments` books an open slot → slot becomes `booked`, appointment `scheduled`.
- [ ] Second appointment with same doctor → `409`; booking a taken slot → `409`.
- [ ] Concurrent bookings of one slot → exactly one succeeds (`-race`).
- [ ] Emits an appointment-created event.

**Verification:**
- [ ] `go test -race ./internal/appointments -run Book` (includes concurrency test).

**Dependencies:** 1.1
**Files:** `internal/appointments/booking.go`, `internal/api/appointments.go`, `internal/appointments/booking_test.go`
**Scope:** M

## Task 1.3: Next appointments (doctor)

**Description:** Doctor lists upcoming appointments, soonest first.

**Acceptance criteria:**
- [ ] `GET /v1/appointments/next` returns future appointments for the doctor, sorted by start.
- [ ] Past/cancelled excluded.

**Verification:**
- [ ] `go test ./internal/appointments -run Next` + httptest.

**Dependencies:** 1.2
**Files:** `internal/appointments/queries.go`, `internal/api/appointments.go` (add route), `internal/appointments/queries_test.go`
**Scope:** S

## Task 1.4: Add note (doctor, manual)

**Description:** Doctor adds a manual note to an appointment; emits `note_added`.

**Acceptance criteria:**
- [ ] `POST /v1/appointments/{id}/notes` (doctor-only) stores a note (`source=manual`).
- [ ] Publishes a `note_added` event carrying `noteId`, `noteText`, `appointmentId`, `patientId`, `actorId`, `timestamp`.

**Verification:**
- [ ] `go test ./internal/appointments -run Note` asserts stored note + event published.

**Dependencies:** 1.2, 0.5
**Files:** `internal/appointments/notes.go`, `internal/api/notes.go`, `internal/appointments/notes_test.go`
**Scope:** S

## Task 1.5: Follow-up prescription (doctor)

**Description:** Doctor issues a prescription on an appointment; emits
`prescription_added`.

**Acceptance criteria:**
- [ ] `POST /v1/appointments/{id}/prescriptions` stores an `active` prescription with `medication`, `issuedAt`, `expiresAt`.
- [ ] Publishes a `prescription_added` event with `prescriptionId`, `medication`, `expiresAt`.

**Verification:**
- [ ] `go test ./internal/appointments -run Prescription`.

**Dependencies:** 1.2, 0.5
**Files:** `internal/appointments/prescriptions.go`, `internal/api/prescriptions.go`, `internal/appointments/prescriptions_test.go`
**Scope:** S

## Task 1.6: Appointment overview (both)

**Description:** Full appointment view including notes + prescriptions, visible to
the doctor and the booking patient.

**Acceptance criteria:**
- [ ] `GET /v1/appointments/{id}` returns appointment + ordered notes + prescriptions.
- [ ] A patient can only see their own appointment; other patients → `403/404`.

**Verification:**
- [ ] `go test ./internal/appointments -run Overview` + httptest authz test.

**Dependencies:** 1.4, 1.5
**Files:** `internal/appointments/overview.go`, `internal/api/appointments.go` (route), `internal/appointments/overview_test.go`
**Scope:** S

### ✅ Checkpoint B — Appointments end-to-end
- [ ] Register slot → book → add note → prescribe → overview shows everything.
- [ ] Booking + concurrency invariants pass `-race`.
- [ ] Events emitted for note/prescription (verified via test subscriber).
- [ ] **Human review.**

---

# Phase 2 — Live Updates / Webhooks (Feature 3)

## Task 2.1: Webhook subscription registry

**Description:** Patient registers/removes a webhook URL with selected event types;
server returns id + generated `secret`.

**Acceptance criteria:**
- [ ] `POST /v1/webhooks` validates URL (http/https, not private/loopback in prod-mode flag), stores subscription, returns `secret`.
- [ ] `DELETE /v1/webhooks/{id}` removes it; only owner patient may modify.

**Verification:**
- [ ] `go test ./internal/webhooks -run Registry` + httptest.

**Dependencies:** Checkpoint B
**Files:** `internal/webhooks/registry.go`, `internal/api/webhooks.go`, `internal/webhooks/registry_test.go`
**Scope:** M

## Task 2.2: Bounded async dispatcher (worker pool + retry + HMAC)

**Description:** In-memory bounded queue + worker pool that POSTs events to
subscriber URLs with timeout, exponential backoff + jitter, and an
`X-Signature` HMAC header. Reuses the filequeue bounded-queue/backpressure pattern.

**Acceptance criteria:**
- [ ] Enqueue is non-blocking under normal load; when the queue is full it applies backpressure (bounded), never unbounded growth.
- [ ] Delivery retries on non-2xx / timeout up to N attempts, then dead-letters (logged).
- [ ] HMAC signature verifiable with the subscription `secret`.
- [ ] Graceful drain on shutdown.

**Verification:**
- [ ] `go test -race ./internal/webhooks -run Dispatch` with an `httptest` receiver (asserts payload, retry on 5xx, slow receiver doesn't block enqueue).

**Dependencies:** 2.1
**Files:** `internal/webhooks/dispatcher.go`, `internal/webhooks/dispatcher_test.go`
**Scope:** M

## Task 2.3: Wire dispatcher to the event stream (embedded by default)

**Description:** Subscribe the dispatcher to the event `Publisher`; map internal
events → the brief's webhook payload for `note_added` / `prescription_added`;
fan out to matching subscribers of the event's patient. **Embedded by default**
(in-process subscriber). Also add a thin `cmd/notifier` that runs the *same*
subscriber against the hub's `/internal/events` SSE when `-embed-workers=false` —
optional split mode, no logic change.

**Acceptance criteria:**
- [ ] A manual `POST …/notes` (from Phase 1) now produces a `note_added` POST to the patient's webhook with the **exact** brief schema (embedded mode).
- [ ] Only subscriptions matching `patientId` + `eventType` receive it.
- [ ] `cmd/notifier` consuming `/internal/events` produces identical behaviour (split mode) — same `Subscriber` code.

**Verification:**
- [ ] End-to-end httptest (embedded): add note → receiver gets correctly-shaped POST.
- [ ] `cmd/notifier` smoke test against a stub `/internal/events` stream.

**Dependencies:** 2.2, 1.4, 1.5, 0.7
**Files:** `internal/webhooks/subscriber.go`, `cmd/server/main.go` (wiring + `-embed-workers`), `cmd/notifier/main.go`, `internal/webhooks/subscriber_test.go`
**Scope:** M

### ✅ Checkpoint C — Live updates
- [ ] Note/prescription events reach a registered webhook with correct payload.
- [ ] Slow/failing subscriber never blocks the API request path; retries + DLQ work.
- [ ] **Human review.**

---

# Phase 3 — Notes Streaming / Transcription (Feature 2)

## Task 3.1: SSE client consumer

**Description:** An HTTP client that opens a long-lived `text/event-stream` to the
transcription server for an appointment and yields parsed `data:` chunks.

**Acceptance criteria:**
- [ ] Parses `data: {json}` lines into `TranscriptChunk{appointmentId,sequence,text,isFinal}`; ignores comments/blank lines.
- [ ] Handles multi-line buffering and stream close/error.

**Verification:**
- [ ] `go test ./internal/transcription -run SSE` against an `httptest` server emitting the brief's example lines.

**Dependencies:** Checkpoint C
**Files:** `internal/transcription/sse.go`, `internal/transcription/sse_test.go`
**Scope:** M

## Task 3.2: Sequence assembler (ordered, gap-aware, bounded-wait, explicit incomplete)

**Description:** Buffers chunks per appointment and orders by `sequence`. Marks the
note **complete** only when `0..finalSeq` are contiguous; on a persistent gap after
a bounded timeout, marks it **incomplete** with the missing sequence numbers.
Never silently finalizes over a gap; never waits forever. Uses the injected
`Clock` so the timeout is deterministic in tests.

**Acceptance criteria:**
- [ ] Complete only when every sequence in `[0, finalSeq]` is present (`finalSeq` = the sequence carrying `isFinal:true`).
- [ ] Out-of-order and duplicate (identical text) sequences tolerated; result is idempotent on replay.
- [ ] Duplicate `sequence` with **different** text → flagged protocol violation (never silently overwritten).
- [ ] Gap unfilled at `isFinal` + timeout → `status=incomplete`, `missing=[…]`, yields a `note_incomplete` outcome (no `note_added`).
- [ ] Bounded timeout is driven by the fake clock → deterministic, no real sleeps.

**Verification:**
- [ ] `go test -race ./internal/transcription -run Assembler` — table-driven cases:
  in-order complete; out-of-order→complete; duplicate→idempotent; gap fills before
  final→complete; gap unfilled at final→**incomplete** (missing=[2], `note_incomplete`);
  conflicting duplicate→violation.

**Dependencies:** 3.1, 0.4 (Clock)
**Files:** `internal/transcription/assembler.go`, `internal/transcription/assembler_test.go`
**Scope:** M

## Task 3.3: Start-transcription endpoint + worker (embedded by default)

**Description:** `POST /v1/appointments/{id}/transcription` returns `202` and, in
the **default embedded** mode, launches an in-process worker goroutine (SSE
consumer → assembler). On a **complete** assembly it stores the note
(`source=dictation`, `status=complete`) and publishes `note_added` (→ webhook +
audit + analytics); on an **incomplete** assembly it stores `status=incomplete` and
publishes `note_incomplete` (→ audit + alert, no `note_added`). A thin
`cmd/transcriber` runs the *same* worker as a separate process in split mode:
it pulls jobs from `/internal/transcription/jobs` and POSTs the assembled note back
via `/internal/appointments/{id}/notes` — no worker-logic change.

**Acceptance criteria:**
- [ ] Endpoint returns `202` immediately; work continues in background with lifecycle/cancel on shutdown.
- [ ] Complete stream → note stored `complete` and a `note_added` event flows to the patient's webhook (full chain).
- [ ] Stream with an unfilled gap → note stored `incomplete` (with `missing`) and a `note_incomplete` event; **no** `note_added` webhook.
- [ ] Duplicate start for an in-flight appointment is rejected/idempotent.
- [ ] `cmd/transcriber` (split mode) produces identical results via the internal contract.

**Verification:**
- [ ] E2E `httptest` (embedded): fake transcription server streams chunks → note stored → webhook receiver gets `note_added`; a separate case streams a gap → `incomplete` + no `note_added` (`go test -race ./test -run Transcription`).

**Dependencies:** 3.2, 2.3
**Files:** `internal/transcription/worker.go`, `internal/api/transcription.go`, `cmd/server/main.go` (wiring), `cmd/transcriber/main.go`, `test/transcription_test.go`
**Scope:** M

### ✅ Checkpoint D — Transcription
- [ ] Start → stream → ordered assembly → stored note → webhook, end-to-end, `-race` clean (embedded default).
- [ ] Split mode (`cmd/transcriber`) verified via the internal contract.
- [ ] **Human review.**

---

# Phase 4 — Pharmacist Medicine Dispatch (Feature 5)

## Task 4.1: Active-prescription query

**Description:** List prescriptions that are **active** = not dispatched AND not
expired (evaluated against injected `Clock`).

**Acceptance criteria:**
- [ ] `GET /v1/prescriptions?status=active` (pharmacist) returns only active ones; expired/dispatched excluded.
- [ ] Deterministic via fake clock in tests.

**Verification:**
- [ ] `go test ./internal/appointments -run ActivePrescriptions` (fake clock).

**Dependencies:** Checkpoint D (needs prescriptions from 1.5)
**Files:** `internal/appointments/prescriptions.go` (query), `internal/api/prescriptions.go` (route), `internal/appointments/prescriptions_query_test.go`
**Scope:** S

## Task 4.2: Dispatch — exactly-once state transition

**Description:** Pharmacist dispatches an active prescription; `active→dispatched`
exactly once, concurrency-safe; records a `Dispatch`; emits an event.

**Acceptance criteria:**
- [ ] `POST /v1/prescriptions/{id}/dispatch` succeeds once; expired/already-dispatched → `409`.
- [ ] Two concurrent dispatches → one `200`, one `409` (`-race`).
- [ ] Dispatch recorded (pharmacist, time); event published.

**Verification:**
- [ ] `go test -race ./internal/appointments -run Dispatch` (concurrency test).

**Dependencies:** 4.1
**Files:** `internal/appointments/dispatch.go`, `internal/api/dispatch.go`, `internal/appointments/dispatch_test.go`
**Scope:** M

### ✅ Checkpoint E — Dispatch
- [ ] Active query correct; dispatch exactly-once under `-race`. **Human review.**

---

# Phase 5 — Historical Overview (Feature 4)

## Task 5.1: Diagnoses — diagnose + dismiss (doctor)

**Description:** Doctor diagnoses a disease and can dismiss it (soft close via
`dismissedAt`); both emit events.

**Acceptance criteria:**
- [ ] `POST /v1/patients/{id}/diagnoses` creates a diagnosis; `DELETE …/{did}` sets `dismissedAt` (no hard delete).
- [ ] Both actions publish events (for audit/history).

**Verification:**
- [ ] `go test ./internal/appointments -run Diagnosis` (or a `clinical` package).

**Dependencies:** Checkpoint E
**Files:** `internal/clinical/diagnoses.go`, `internal/api/diagnoses.go`, `internal/clinical/diagnoses_test.go`
**Scope:** M

## Task 5.2: Point-in-time overview — fold the event log at `?at=`

**Description:** `GET /v1/patients/{id}/overview?at=<RFC3339>` returns, as-of `at`:
diagnosed (not-yet-dismissed) diseases, prescriptions active at `at`, and
appointments with their notes at `at`. Implemented by folding events with
`timestamp ≤ at`.

**Acceptance criteria:**
- [ ] `at` defaults to now; results reflect only events up to `at`.
- [ ] A diagnosis dismissed after `at` still appears; a prescription expired after `at` still counts active at `at`.

**Verification:**
- [ ] `go test ./internal/clinical -run Overview` with events at t0/t1/t2 and fake clock.

**Dependencies:** 5.1
**Files:** `internal/clinical/overview.go`, `internal/api/overview.go`, `internal/clinical/overview_test.go`
**Scope:** M

### ✅ Checkpoint F — Historical overview
- [ ] Point-in-time overview correct across multiple timestamps. **Human review.**

---

# Phase 6 — Audit Trail (Feature 6)

## Task 6.1: Audit query endpoint (view over the event log)

**Description:** Every mutation already appends an event with `actorId` +
`timestamp` (since Phase 0). Expose a filtered read view.

**Acceptance criteria:**
- [ ] `GET /v1/audit?patientId=&from=&to=&entity=` returns matching events (who/what/when), tenant-scoped, doctor/admin only.
- [ ] Confirms coverage: booking, note, prescription, dispatch, diagnosis all produce an audit-visible record.

**Verification:**
- [ ] `go test ./internal/audit -run Audit` asserts each mutation type appears with actor + timestamp.

**Dependencies:** Phases 1–5 (producers exist)
**Files:** `internal/audit/query.go`, `internal/api/audit.go`, `internal/audit/query_test.go`
**Scope:** S

### ✅ Checkpoint G — Audit
- [ ] All patient-data mutations auditable with actor + time. **Human review.**

---

# Phase 7 — Usage Analytics (Feature 7)

## Task 7.1: Per-tenant analytics aggregation

**Description:** Maintain per-tenant counters (subscriber over the event log):
total appointments, active patients, prescription counts.

**Acceptance criteria:**
- [ ] Counters update as events arrive; correct after a mix of operations.
- [ ] "Active patients" uses the configured rolling-window definition.

**Verification:**
- [ ] `go test -race ./internal/analytics -run Counters`.

**Dependencies:** Checkpoint G
**Files:** `internal/analytics/counters.go`, `internal/analytics/counters_test.go`
**Scope:** M

## Task 7.2: Analytics query endpoint

**Description:** `GET /v1/analytics` returns the tenant's totals.

**Acceptance criteria:**
- [ ] Returns `{ totalAppointments, activePatients, prescriptionCounts }` for the caller's tenant only.

**Verification:**
- [ ] `go test ./internal/api -run Analytics` (httptest, two tenants isolated).

**Dependencies:** 7.1
**Files:** `internal/api/analytics.go`, `internal/api/analytics_test.go`
**Scope:** S

### ✅ Checkpoint H — Analytics
- [ ] Tenant-scoped analytics correct and isolated. **Human review.**

---

# Phase 8 — Multi-Tenancy Hardening + Shipping (Feature 8)

## Task 8.1: Cross-cutting tenant-isolation test suite

**Description:** A dedicated test that, for every mutating + reading endpoint,
proves tenant A cannot observe tenant B's data.

**Acceptance criteria:**
- [ ] Matrix test across all endpoints: B's data invisible to A (404/empty), never leaked.

**Verification:**
- [ ] `go test -race ./test -run TenantIsolation`.

**Dependencies:** Phases 1–7
**Files:** `test/tenant_isolation_test.go`
**Scope:** M

## Task 8.2: Scaling design write-up (design-only)

**Description:** Document horizontal scaling: stateless service, CockroachDB
partitioned by `tenantId`, region-pinned ranges, optional shard-per-large-tenant,
path to 10 M users/tenant. No code.

**Acceptance criteria:**
- [ ] `docs/design-multitenancy.md` covers isolation model, independent scaling, regions, and the in-memory→DB migration path.

**Verification:**
- [ ] Doc review.

**Dependencies:** none (parallelizable)
**Files:** `docs/design-multitenancy.md`
**Scope:** S

## Task 8.3: Full-flow integration test + README + config flags

**Description:** One E2E test covering book→transcribe→webhook→prescribe→dispatch→
overview→audit→analytics; server config flags (`-addr`, timeouts, queue sizes);
README run instructions.

**Acceptance criteria:**
- [ ] `go test -race ./test -run FullFlow` green.
- [ ] `make check` green; README documents how to run + curl examples.

**Verification:**
- [ ] `make check`.

**Dependencies:** 8.1
**Files:** `test/full_flow_test.go`, `cmd/server/main.go` (flags), `README-healthcare.md`
**Scope:** M

### ✅ Checkpoint I — Complete
- [ ] All acceptance criteria met; `make check` green; docs updated. **Final review.**

---

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Scope explosion (8 features, 4 h) | High | Strict phase order; F4/F7/F8 partial-by-design; stop at any checkpoint with a coherent slice. |
| Concurrency bugs (booking, dispatch) | High | Single-lock check-and-set; `-race` concurrency tests written **with** the feature (1.2, 4.2). |
| Webhook slowness leaks into request path | Med | Bounded queue + workers from the start (2.2); enqueue never blocks handlers. |
| Out-of-order / gapped transcription | Med | Assembler unit-tested first (3.2) before wiring the endpoint. |
| Retrofitting audit/history/analytics | High | Event log is **foundational** (0.5); producers emit from Phase 1; consumers added later with zero producer rework. |
| Non-deterministic time in tests | Med | Injectable `Clock`/`IDGen` (0.4) for expiry + point-in-time. |
| Transcription server contract unknown | Med | Code against a `text/event-stream` fake; interface lets the real endpoint drop in. |

---

## Parallelization Opportunities

- **Sequential (hard):** Phase 0 before everything; 1.1→1.2 (booking needs slots);
  2.1→2.2→2.3; 3.1→3.2→3.3; 4.1→4.2; 5.1→5.2.
- **Safe to parallelize after Checkpoint B:** Phase 2 (webhooks) and Phase 4
  (dispatch) are independent; Task 8.2 (scaling doc) any time; audit/analytics
  read models (Phases 6–7) once producers exist.
- **Contract-first:** the webhook payload shape and the event schema are defined in
  Phase 0/2 so consumers (F6/F7) can be built against them.

---

## Resolved Decisions

- **Concurrency model → mutex + one critical section per invariant** (not
  actor/channel). Rationale: stateless scaling means the DB transaction is the real
  authority; the in-memory mutex is a 1:1 stand-in. See Architecture Decision #6 and
  spec §4.7.
- **Assembler gap policy → ordered, gap-aware, bounded-wait, explicitly
  incomplete.** Never silently finalize over a gap (clinical safety); never wait
  forever (determinism). Complete only when `0..finalSeq` contiguous, else
  `incomplete` + `note_incomplete` event. See Architecture Decision #8, Tasks
  3.2/3.3, and spec §5.2.

## Open Questions (carried from the spec)

1. Exact transcription-server URL/auth for opening the per-appointment SSE stream.
2. Webhook retry limits (attempts / backoff ceiling / dead-letter behaviour).
3. "Active patient" definition for analytics (rolling window length).
4. Whether "prescription used" ever means anything other than `dispatched`.
5. Acceptable to keep header-based auth for the exercise (JWT/OAuth2 as prod path)?
6. Assembler bounded-timeout duration (how long to wait for a missing sequence
   before marking `incomplete`) — a tunable, not a design question.

---

## Suggested First Step

On approval, begin **Task 0.1** (module scaffold + `/healthz` + graceful shutdown),
then stop at **Checkpoint A** for review before any feature work.
