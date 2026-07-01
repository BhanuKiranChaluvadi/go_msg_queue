# medconnect — Healthcare Appointment Management Service

A backend service in Go that connects **doctors, patients, and pharmacists**:
patients book appointments and receive live updates; doctors keep notes, dictate
notes via an external transcription stream, issue prescriptions, and diagnose;
pharmacists dispatch medicines. Each **hospital organization is an isolated
tenant**.

> **Scope note.** This service is **backend only — there is no UI by design**;
> every capability is exercised over HTTP/JSON and by an automated test suite.
> The goal is a clean, correct core plus an architecture that visibly
> *accommodates* production concerns.

---

## Capabilities

Each capability attaches to the shared event log and store interfaces; the
production database is a drop-in adapter (see below).

| # | Capability | Status |
|---|---------|--------|
| 1 | Appointments Management | ✅ implemented |
| 2 | Notes Streaming (SSE transcription, gap-aware) | ✅ implemented |
| 3 | Live Updates (async signed webhooks) | ✅ implemented |
| 4 | Historical Overview (diagnoses, point-in-time) | ✅ implemented |
| 5 | Pharmacist Dispatch (exactly-once) | ✅ implemented |
| 6 | Audit Trail (view over the event log) | ✅ implemented |
| 7 | Usage Analytics (aggregate the log) | ✅ implemented |
| 8 | Multi-Tenancy | ✅ logical isolation enforced + tested; DB/region partitioning designed |

---

## Quick Start

Requires **Go 1.26+** (already in the dev container). No database or other
software to install — the core runs entirely on the standard library.

```bash
make run        # start the API server on :8080 (single binary, workers embedded)
make test       # run all unit + integration tests
make race       # run tests with the race detector
make check      # fmt + vet + build + race (the full local gate)
```

| Command | Description |
|---------|-------------|
| `make run` | Start the server (`:8080`), workers embedded |
| `make test` / `make race` | Tests / tests under the race detector |
| `make check` | fmt + vet + build + race |
| `make build` | Compile `./bin/server` |

---

## Using the API

Every `/v1` request is authenticated with two headers:

- `X-Tenant-ID` — the hospital organization (data is isolated per tenant)
- `X-User-ID` — the acting user; their role (doctor / patient / pharmacist)
  determines what they may do

> The running server seeds three demo users in tenant `demo`: `doctor`, `patient`,
> and `pharmacist`. (In production this header-based identity is replaced by
> JWT/OAuth2 with the same resolver seam.)

A complete walkthrough with `curl` (start the server with `make run` first):

```bash
BASE=localhost:8080
DOC='-H X-Tenant-ID:demo -H X-User-ID:doctor'
PAT='-H X-Tenant-ID:demo -H X-User-ID:patient'
PHARM='-H X-Tenant-ID:demo -H X-User-ID:pharmacist'

# health check
curl $BASE/healthz

# 1) doctor publishes an availability slot  -> returns the slot (note its "id")
curl -X POST $BASE/v1/timeslots $DOC \
  -d '{"start":"2027-01-01T09:00:00Z","end":"2027-01-01T09:30:00Z"}'

# 2) patient lists the doctor's open slots
curl "$BASE/v1/doctors/doctor/timeslots" $PAT

# 3) patient books a slot  -> returns the appointment (note its "id")
curl -X POST $BASE/v1/appointments $PAT \
  -d '{"doctorId":"doctor","timeslotId":"<SLOT_ID>"}'

# 4) patient registers a webhook for live updates (point at your receiver)
curl -X POST $BASE/v1/webhooks $PAT \
  -d '{"url":"https://example.test/hook","eventTypes":["note_added","prescription_added"]}'

# 5) doctor adds a note and issues a prescription on the appointment
curl -X POST $BASE/v1/appointments/<APPT_ID>/notes $DOC \
  -d '{"text":"Patient reports headache."}'
curl -X POST $BASE/v1/appointments/<APPT_ID>/prescriptions $DOC \
  -d '{"medication":"Aspirin 100mg","expiresAt":"2027-02-01T00:00:00Z"}'

# 6) doctor starts streamed dictation (needs a transcription server; see flags)
curl -X POST $BASE/v1/appointments/<APPT_ID>/transcription $DOC

# 7) pharmacist lists active prescriptions and dispatches one
curl "$BASE/v1/prescriptions?status=active" $PHARM
curl -X POST $BASE/v1/prescriptions/<RX_ID>/dispatch $PHARM

# 8) doctor diagnoses the patient
curl -X POST $BASE/v1/patients/patient/diagnoses $DOC -d '{"disease":"Migraine"}'

# 9) overviews
curl $BASE/v1/appointments/<APPT_ID> $PAT                       # appointment + notes + rx
curl "$BASE/v1/patients/patient/overview?at=2027-01-02T00:00:00Z" $DOC   # point-in-time

# 10) audit trail and usage analytics (doctor / org staff)
curl "$BASE/v1/audit?patientId=patient" $DOC
curl $BASE/v1/analytics $DOC
```

Responses are JSON; errors use `{"error":{"code","message"}}` with the matching
HTTP status (`400/401/403/404/409`). Collection endpoints (the `GET` lists above)
return a `{"data": [...]}` envelope so the shape can grow (e.g. pagination
metadata) without breaking existing clients.

### Configuration flags

```bash
./bin/server \
  -addr :8080 \                       # HTTP listen address
  -internal-token <token> \           # guards the /internal/* worker stream
  -transcription-url <url> \          # external transcription SSE server (for dictation)
  -transcription-token <token> \      # bearer token for that server
  -embed-workers=true                 # run the workers in-process (default)
```

Environment equivalents: `MEDCONNECT_INTERNAL_TOKEN`,
`MEDCONNECT_TRANSCRIPTION_URL`, `MEDCONNECT_TRANSCRIPTION_TOKEN`.

---

## API

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
| `POST /v1/appointments/{id}/transcription` | doctor | Start streamed dictation (202, background) |
| `POST /v1/webhooks` · `DELETE /v1/webhooks/{id}` | patient | Register / remove a live-update webhook |
| `GET /v1/prescriptions?status=active` | pharmacist | Active-prescription worklist |
| `POST /v1/prescriptions/{id}/dispatch` | pharmacist | Dispatch a prescription (exactly-once) |
| `POST /v1/patients/{id}/diagnoses` · `DELETE .../{did}` | doctor | Diagnose / dismiss |
| `GET /v1/patients/{id}/overview?at=` | patient/doctor | Point-in-time clinical overview |
| `GET /v1/audit?patientId=&type=&from=&to=` | doctor | Audit trail (who changed what, when) |
| `GET /v1/analytics` | doctor | Tenant usage summary |
| `GET /internal/events` | worker (token) | SSE event fan-out (split mode) |

Events on the log: `appointment_booked`, `note_added`, `note_incomplete`,
`prescription_added`, `prescription_dispatched`, `diagnosis_added`,
`diagnosis_dismissed`.

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
 │    NOW  →  in-memory (maps + RWMutex)          │   ← the core
 │    PROD →  PostgreSQL / CockroachDB            │   ← drop-in adapter
 │            (per-tenant, region-partitioned)    │
 └───────────────────────────────────────────────┘

 external transcription server ──SSE (data: {...})──► transcription worker
                                                        (embedded, or cmd/transcriber)
```

**Three communication channels, three fit-for-purpose protocols:**

| Channel | Direction | Protocol | Why |
|---------|-----------|----------|-----|
| Client API | client → us | **REST + JSON** | stdlib, universal, trivially testable |
| Transcription | external → us | **SSE (consumed)** | the source already emits `data: {...}` — that *is* SSE; a `bufio.Scanner` loop, no library |
| Live updates | us → patient | **outbound webhook (HTTP POST)** | delivered async so it never blocks the request path |

---

## Do we use a database? (and how do we scale without one?)

**Today: no running database.** The core persists to Go maps guarded by a
`sync.RWMutex`, hidden behind six `Repository` interfaces (`internal/store`).

**Why in-memory**

- Zero infrastructure to stand up → all effort goes into domain logic.
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
database. At the production targets (**10 M users/tenant, multi-region**):

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

Short version of the "why".

1. **REST + SSE + outbound webhooks (not WebSocket/gRPC).** We are never a
   streaming *server* — we *consume* one SSE stream and *send* small POSTs — so
   there is nothing heavy to build. SSE also matches the transcription format
   exactly. WebSocket (bidirectional, needs a handshake/lib) and gRPC (protoc +
   codegen + a dependency) would cost setup time for capabilities we don't need.

2. **One append-only event log unifies three features.** Every mutation emits an
   immutable, timestamped event carrying *who* + *what* + *when*. That single
   structure yields **Live Updates** (subscribers), **Audit** (the log *is* the
   trail), and **Analytics** (aggregate the log) — build once, get three.
   Producers never change when a new consumer is added. (The **Historical
   Overview** reads point-in-time state directly from each entity's timestamps —
   equally correct and simpler than replaying the log.)

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

6. **Simplified auth.** Identity comes from `X-Tenant-ID` /
   `X-User-ID` headers resolved to an actor + role. Production swaps in JWT/OAuth2
   with the same `ActorResolver` seam; nothing else changes.

7. **Webhook delivery is async and bounded**: a slow/failing patient
   endpoint must never block a doctor's request, so delivery uses a bounded queue
   + worker pool with retry/backoff.

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
```

Layering (hexagonal): `domain` ← `store` ports ← `services` ← `api`. Handlers are
thin; rules live in services; I/O is behind interfaces.

---

## Testing

- **Framework:** stdlib `testing` + `net/http/httptest`; deterministic via
  injectable `Clock`/`IDGen`.
- **Coverage style:** every feature is tested at the **service** level (unit) and
  the **HTTP** level (integration), with cases spanning **multiple patients,
  multiple doctors, and multiple hospitals (tenants)**, plus `-race` concurrency
  proofs for booking invariants.
- Run: `make race` (or `make check` for the full gate). All packages are green.
```
