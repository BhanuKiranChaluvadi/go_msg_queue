# medconnect — Healthcare Appointment Management Service

A backend service in Go that connects **doctors, patients, and pharmacists**:
patients book appointments and receive live updates; doctors keep notes, dictate
notes via an external transcription stream, issue prescriptions, and diagnose;
pharmacists dispatch medicines. Each **hospital organization is an isolated
tenant**.

> **Scope note.** This is a ~4-hour technical assessment. It is **backend only —
> there is no UI by design**; every capability is exercised over HTTP/JSON and by
> an automated test suite. The brief lists eight feature areas; the goal is a
> clean, correct core plus an architecture that visibly *accommodates* the rest.
> Full requirements: [docs/Question.md](docs/Question.md). Design & plan:
> [docs/specifications-healthcare.md](docs/specifications-healthcare.md),
> [docs/plan-healthcare.md](docs/plan-healthcare.md).

---

## Quick Start

Requires **Go 1.26+** (already in the dev container). No database or other
software to install — the core runs entirely on the standard library.

```bash
make run        # start the API hub on :8080 (single binary, workers embedded)
make test       # run all unit + integration tests
make race       # run tests with the race detector
make check      # fmt + vet + build + race (the full local gate)
```

Try it (the dev server seeds demo users in tenant `demo`):

```bash
# health
curl localhost:8080/healthz

# doctor registers an availability slot
curl -X POST localhost:8080/v1/timeslots \
  -H 'X-Tenant-ID: demo' -H 'X-User-ID: doctor' \
  -d '{"start":"2027-01-01T09:00:00Z","end":"2027-01-01T09:30:00Z"}'

# patient books it, then views the full overview
curl -X POST localhost:8080/v1/appointments \
  -H 'X-Tenant-ID: demo' -H 'X-User-ID: patient' \
  -d '{"doctorId":"doctor","timeslotId":"<id-from-above>"}'
```

| Command | Description |
|---------|-------------|
| `make run` | Start the hub (`:8080`), workers embedded |
| `make test` / `make race` | Tests / tests under the race detector |
| `make check` | fmt + vet + build + race |
| `make build` | Compile `./bin/server` |

> Split mode (running the transcription/webhook workers as separate processes) is
> a planned, config-flag option (`-embed-workers=false`); the worker binaries land
> with Phases 2–3.

---

## Architecture Overview

A **modular monolith**: one deployable binary with clean, interface-separated
packages. Requests flow through a thin HTTP layer into services that own the
business rules; every state change is written once to an **append-only event
log**, from which live updates, audit, history, and analytics are derived.
Storage sits behind `Repository` interfaces so the persistence engine can change
without touching business logic.

```
        Doctors / Patients / Pharmacists                 (no UI — HTTP clients / curl / tests)
                     │  REST + JSON
                     ▼
 ┌───────────────────────────────────────────────────────────────┐
 │                    cmd/server  (the hub)                        │
 │                                                                 │
 │   api        →  routing · auth (tenant+role) · JSON envelope    │
 │      │                                                          │
 │   services  →  appointments (timeslots, booking, notes,         │
 │      │           prescriptions, overview) …                     │
 │      │                                                          │
 │   events    →  append-only log + in-process publisher ──────────┼──► webhook dispatcher
 │      │            (unifies live updates / audit / history /      │      (embedded goroutine,
 │      │             analytics)                                    │       or cmd/notifier) ──► patient webhook URLs
 │      │                                                          │
 │   store     →  Repository interfaces (ports)                    │
 │      │                                                          │
 └──────┼──────────────────────────────────────────────────────────┘
        │  adapter (dependency inversion)
        ▼
 ┌───────────────────────────────────────────────┐
 │  Persistence                                   │
 │    NOW  →  in-memory (maps + RWMutex)          │   ← the 4h core
 │    PROD →  PostgreSQL / CockroachDB            │   ← drop-in adapter
 │            (per-tenant, region-partitioned)    │
 └───────────────────────────────────────────────┘

 external transcription server ──SSE (data: {...})──► transcription worker  (Phase 3)
                                                        (embedded, or cmd/transcriber)
```

**Three communication channels, three fit-for-purpose protocols:**

| Channel | Direction | Protocol | Why |
|---------|-----------|----------|-----|
| Client API | client → us | **REST + JSON** | stdlib, universal, trivially testable |
| Transcription | external → us | **SSE (consumed)** | the source already emits `data: {...}` — that *is* SSE; a `bufio.Scanner` loop, no library |
| Live updates | us → patient | **outbound webhook (HTTP POST)** | required by the brief; delivered async so it never blocks the request path |

---

## Do we use a database? (and how do we scale without one?)

**Today: no running database.** The core persists to Go maps guarded by a
`sync.RWMutex`, hidden behind six `Repository` interfaces (`internal/store`).

**Why in-memory for the assessment**

- Zero infrastructure to stand up → all four hours go into domain logic.
- Standard-library-only core → nothing to install, trivial to review and run.
- Deterministic and fast under `go test -race` (with injectable `Clock`/`IDGen`).

**Why this is not a dead end.** The in-memory store is a *faithful stand-in for a
database*, not an architectural shortcut:

- Services depend on the **interfaces**, never the concrete store (dependency
  inversion). A `postgres`/`cockroach` adapter drops in with **no service
  changes** — the compile-time contract is already there.
- Booking and dispatch do their check-and-set inside **one mutex critical
  section**, which maps **1:1 onto a database transaction**
  (`SELECT … FOR UPDATE` / serializable isolation).
- Every write also appends to the event log — in production that log becomes an
  append-only `events` table (or a stream), written in the same transaction.

**How the design scales (production target).** The service is **stateless**, so
it scales horizontally behind a load balancer; all durable state lives in the
database. For the brief's targets (**10 M users/tenant, multi-region**):

- **CockroachDB** (Go-friendly, horizontally scalable, geo-distributed) is the
  intended engine, with data **partitioned by `tenantId`** and **region-pinned**
  ranges for residency and latency.
- **Multi-tenancy** is enforced in code today: `tenantId` flows through
  `context.Context` and every store call is tenant-scoped, so no query can cross
  tenants. The DB partitioning is the physical expression of that same boundary.
- Independent scaling per tenant/workload is why the **worker split** exists (see
  below): the streaming and webhook workloads can move to their own processes
  when load justifies it.

In short: **in-memory now, database-ready by construction, stateless-to-scale by
design.**

---

## Key Decisions & Rationale

Short version of the "why". Fuller reasoning lives in
[docs/specifications-healthcare.md](docs/specifications-healthcare.md).

1. **REST + SSE + outbound webhooks (not WebSocket/gRPC).** We are never a
   streaming *server* — we *consume* one SSE stream and *send* small POSTs — so
   there is nothing heavy to build. SSE also matches the transcription format
   exactly. WebSocket (bidirectional, needs a handshake/lib) and gRPC (protoc +
   codegen + a dependency) would cost setup time for capabilities we don't need.

2. **One append-only event log unifies four features.** Every mutation emits an
   immutable, timestamped event carrying *who* + *what* + *when*. That single
   structure yields **Live Updates** (subscribers), **Audit** (the log *is* the
   trail), **Historical Overview** (fold events up to time *T*), and **Analytics**
   (aggregate the log) — build once, get four. Producers never change when a new
   consumer is added.

3. **In-memory stores behind interfaces (DIP).** Keeps the core dependency-light
   and testable; makes the database a later adapter, not a rewrite. (See the DB
   section above.)

4. **Concurrency: a mutex per invariant, not an actor model.** Because the
   service must be stateless to scale, the real concurrency authority in
   production is the DB transaction; an in-memory mutex is its faithful stand-in.
   An actor-per-aggregate model implies single-process *stateful* ownership, which
   contradicts stateless scaling and would have to be undone. The mutex is also
   simpler and `-race`-provable (proven by concurrent booking tests).

5. **Modular monolith with an optional, reversible worker split.** Default: one
   binary with the transcription and webhook workers embedded as goroutines
   (simplest to run/test, atomic deploys). Optional: run them as separate
   processes (`cmd/transcriber`, `cmd/notifier`) that talk to the hub over a
   *tiny, versioned* internal contract (three `/internal/*` endpoints) — the
   `reader/writer/broker` shape, **no Docker**. The split becomes worthwhile only
   once state is externalized and a workload needs independent scaling; keeping it
   a config flag (`-embed-workers`) avoids committing to either extreme.

6. **Simplified auth for the exercise.** Identity comes from `X-Tenant-ID` /
   `X-User-ID` headers resolved to an actor + role. Production swaps in JWT/OAuth2
   with the same `ActorResolver` seam; nothing else changes.

7. **Webhook delivery is async and bounded** (Phase 2): a slow/failing patient
   endpoint must never block a doctor's request, so delivery uses a bounded queue
   + worker pool with retry/backoff — the backpressure idea reused as a pattern.

---

## Project Structure

```
cmd/server/            → composition root: wires deps, starts the hub
internal/
  domain/              → entities, status enums, invariants (pure, no I/O)
  store/               → Repository interfaces (ports) + in-memory adapters
    memory/            → generic tenant-partitioned Store[T] (maps + RWMutex)
  appointments/        → services: timeslots, booking, notes, prescriptions, overview
  events/              → append-only event log + in-process publisher
  tenancy/             → tenant/actor context, auth middleware, role guard
  platform/            → Clock + IDGen (real + fake, for deterministic tests)
  protocol/            → tiny internal contract + reusable SSE client
  api/                 → HTTP routing, middleware, JSON envelope, handlers
docs/                  → the brief, specification, plan (design rationale)
```

Layering (hexagonal): `domain` ← `store` ports ← `services` ← `api`. Handlers are
thin; rules live in services; I/O is behind interfaces.

---

## API (implemented so far — Feature 1)

All `/v1` routes require `X-Tenant-ID` + `X-User-ID`. Errors use a consistent
`{"error":{"code","message"}}` envelope.

| Method & Path | Role | Purpose |
|---|---|---|
| `POST /v1/timeslots` | doctor | Register an availability slot |
| `GET /v1/doctors/{id}/timeslots` | any | List a doctor's open slots |
| `POST /v1/appointments` | patient | Book a slot (≤1 per patient-doctor, no double-book) |
| `GET /v1/appointments/next` | doctor | Upcoming appointments |
| `GET /v1/appointments/{id}` | participants | Overview: appointment + notes + prescriptions |
| `POST /v1/appointments/{id}/notes` | doctor | Add a manual note |
| `POST /v1/appointments/{id}/prescriptions` | doctor | Issue a prescription |
| `GET /internal/events` | worker (token) | SSE event fan-out (split mode) |

Events emitted today: `appointment_booked`, `note_added`, `prescription_added`.

---

## Testing

- **Framework:** stdlib `testing` + `net/http/httptest`; deterministic via
  injectable `Clock`/`IDGen`.
- **Coverage style:** every feature is tested at the **service** level (unit) and
  the **HTTP** level (integration), with cases spanning **multiple patients,
  multiple doctors, and multiple hospitals (tenants)**, plus `-race` concurrency
  proofs for booking invariants.
- Run: `make race` (or `make check` for the full gate). All packages are green.

---

## Roadmap (remaining feature areas)

Structure is in place for all of these; they attach to the existing event log and
store interfaces without reworking Feature 1.

| # | Feature | Status |
|---|---------|--------|
| 1 | Appointments Management | ✅ implemented |
| 2 | Live Updates (webhooks) | ▶ next |
| 3 | Notes Streaming (SSE transcription) | designed |
| 4 | Historical Overview (diagnoses, point-in-time) | designed (event-log fold) |
| 5 | Pharmacist Dispatch (exactly-once) | designed |
| 6 | Audit Trail | ✅ event log carries who+when; query endpoint pending |
| 7 | Usage Analytics | designed (aggregate the log) |
| 8 | Multi-Tenancy (DB, regions) | logical isolation done; DB/partitioning designed |
```
