# filequeue

An asynchronous file-to-file messaging system built with Go's standard library only — zero third-party dependencies.

A **reader** streams a file to a **queue server** (broker) over TCP; a **writer** drains it back out to a second file. The transfer is byte-identical: payloads are opaque and never inspected or transformed, so any file (text or binary) round-trips unchanged.

## Architecture

```
┌──────────────┐  TCP (producer)  ┌──────────────────┐  TCP (consumer)  ┌──────────────┐
│    reader    │ ───────────────► │   queue server   │ ───────────────► │    writer    │
│ (reads file) │   framed bytes   │   per-stream     │   framed bytes   │ (writes file)│
└──────────────┘                  │   bounded FIFO   │                  └──────────────┘
   input file                     └──────────────────┘                    output file
```

Each TCP connection declares a **role** (producer or consumer) and a **stream id**. The broker routes every producer's frames into a per-stream bounded FIFO and out to that stream's single consumer, in order. Many independent streams can run concurrently.

| Component | Role | Description |
|-----------|------|-------------|
| `cmd/server` | broker | Accepts connections, routes frames between producers and consumers per stream |
| `cmd/reader` | producer | Reads an input file in fixed-size chunks and sends each as a frame |
| `cmd/writer` | consumer | Receives frames for a stream and appends them to an output file, then fsyncs |

## Project Structure

```
filequeue/
├── cmd/
│   ├── server/          # TCP queue broker (accept loop, per-connection handlers)
│   ├── reader/          # producer worker (file → frames)
│   └── writer/          # consumer worker (frames → file)
├── internal/
│   ├── wire/            # length-prefixed framing + stream-id codec
│   ├── queue/           # bounded, blocking, closeable FIFO
│   └── broker/          # per-stream registry (producer/consumer matching, GC)
├── test/                # end-to-end round-trip tests + testdata
├── docs/                # design, decisions, and specifications
├── .github/workflows/   # CI quality gates + tagged releases
├── Makefile
└── README.md
```

## Prerequisites

- Go 1.26+

## Quick Start

```bash
# Build all binaries into ./bin/
make build

# Run the full pipeline and verify the output matches the input byte-for-byte
make run

# Run unit tests
make test
```

## Running Manually

Start each component in its own terminal. Producers and consumers find each other by **stream id** (default: `default`); a consumer waits up to `-attach-timeout` for a producer to appear.

**1. Queue server**
```bash
./bin/server -addr 127.0.0.1:4000
```

**2. Writer worker** (consumer)
```bash
./bin/writer -addr localhost:4000 -out output.txt -stream default
```

**3. Reader worker** (producer)
```bash
./bin/reader -addr localhost:4000 -in test/testdata/sample.txt -stream default
```

The reader streams the whole file and closes its connection. The broker closes that stream's queue once the producer disconnects; the writer drains any remaining frames, fsyncs, and exits. Verify the result:

```bash
diff test/testdata/sample.txt output.txt
```

## Wire Protocol

A small binary protocol over TCP, defined in `internal/wire`.

**Framing.** Every frame is a 4-byte big-endian `uint32` length prefix followed by exactly that many opaque payload bytes. The length is validated against `MaxFrameSize` (65535) *before* any buffer is allocated, so a malformed length can never trigger a large allocation. Zero-length frames are reserved and rejected.

**Per-connection handshake.** Immediately after connecting, each side sends:

1. one **role byte** — `P` (producer) or `C` (consumer);
2. one **stream-id frame** — a token of 1–64 bytes from `[A-Za-z0-9._-]`.

The server then replies with a one-byte **ack**: `K` when the producer/consumer was attached, or `X` when the stream is already taken or the broker is at capacity. A rejected client reports the error and exits non-zero rather than silently dropping its input. After a `K` ack, a producer sends data frames until it closes the connection; a consumer receives data frames until the stream's queue is closed and drained.

**Routing & lifecycle.** The broker keeps one bounded FIFO per stream id, allowing a single producer and a single consumer each. The fixed queue capacity provides backpressure — when it fills, the producer's socket stalls rather than the broker growing without bound. A stream is reference-counted and garbage-collected once both ends have detached and it has been consumed.

## Configuration Flags

**server**

| Flag | Default | Purpose |
|------|---------|---------|
| `-addr` | `127.0.0.1:4000` | TCP listen address |
| `-idle` | `30s` | Per-connection idle read/write timeout (`0` disables) |
| `-max-streams` | `256` | Maximum concurrent streams (`0` = unlimited) |
| `-max-conns` | `1024` | Maximum concurrent connections (`0` = unlimited) |
| `-attach-timeout` | `10s` | How long a consumer waits for an absent producer (`0` = forever) |

**reader**

| Flag | Default | Purpose |
|------|---------|---------|
| `-in` | *(required)* | Input file path |
| `-addr` | `localhost:4000` | Queue server address |
| `-stream` | `default` | Stream id to publish to |

**writer**

| Flag | Default | Purpose |
|------|---------|---------|
| `-out` | *(required)* | Output file path |
| `-addr` | `localhost:4000` | Queue server address |
| `-stream` | `default` | Stream id to subscribe to |

## Makefile Targets

```
make build   — compile all three binaries into ./bin/
make run     — start server + writer + reader, then verify output == input
make test    — run all unit tests
make cover   — report total test coverage across all packages
make race    — run all tests with the race detector
make vet     — run go vet static analysis
make fmt     — fail if any file is not gofmt-clean
make vuln    — scan for known vulnerabilities (govulncheck)
make check   — run every CI quality gate locally (fmt, vet, build, race, vuln)
make clean   — remove compiled binaries and generated output file
make help    — list available targets
```

## Further Reading

- `docs/internals-explained.md` — a beginner-friendly tour of how `wire`, `queue`, and the registry fit together
- `docs/design-and-decisions.md` — what was built, the choices made and skipped, and the roadmap
- `docs/specifications.md` — the protocol and behavioural specification
