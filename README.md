# filequeue

A simple asynchronous file-to-file messaging system built with Go's standard library only.

## Architecture

```
┌──────────────┐   TCP PUSH    ┌──────────────────┐   TCP POP    ┌──────────────┐
│    reader    │ ────────────► │   queue server   │ ◄─────────── │    writer    │
│ (reads file) │               │  (stdlib TCP)    │              │ (writes file)│
└──────────────┘               └──────────────────┘              └──────────────┘
       ▲  input.txt                                                output.txt  │
       └────────────────────────────────────────────────────────────────────── ┘
```

Three independent components communicate asynchronously over TCP:

| Component | Description |
|-----------|-------------|
| `cmd/server` | In-memory FIFO queue exposed over a custom TCP text protocol |
| `cmd/reader` | Reads an input file line-by-line and PUSHes each line to the server |
| `cmd/writer` | POPs lines from the server and writes them to an output file |

## Project Structure

```
filequeue/
├── cmd/
│   ├── server/          # TCP queue server
│   ├── reader/          # file reader worker
│   └── writer/          # file writer worker
├── internal/
│   └── queue/           # thread-safe in-memory FIFO queue
├── test/
│   └── testdata/        # sample input file
├── Makefile
└── README.md
```

## Prerequisites

- Go 1.21+

## Quick Start

```bash
# Build all binaries
make build

# Run the full pipeline and verify the output matches the input
make run

# Run unit tests
make test
```

## Running Manually

Start each component in a separate terminal:

**1. Queue server**
```bash
./bin/server -addr :4000
```

**2. Writer worker** (connect before the reader so no messages are missed)
```bash
./bin/writer -addr localhost:4000 -out output.txt
```

**3. Reader worker**
```bash
./bin/reader -addr localhost:4000 -in test/testdata/sample.txt
```

The reader sends all lines then sends a `CLOSE` command. The server closes the queue, the writer drains remaining messages and exits. Verify the result:

```bash
diff test/testdata/sample.txt output.txt
```

## Protocol

The server speaks a newline-delimited text protocol over TCP:

| Command      | Response      | Behaviour                                      |
|--------------|---------------|------------------------------------------------|
| `PUSH <msg>` | `OK`          | Enqueue a message                              |
| `POP`        | `MSG <msg>`   | Dequeue next message; blocks until available   |
| `POP`        | `CLOSED`      | Queue is closed and empty                      |
| `CLOSE`      | `OK`          | Close the queue; unblocks all waiting consumers|

## Makefile Targets

```
make build   — compile all three binaries into ./bin/
make run     — start server + writer + reader, then verify output == input
make test    — run all unit tests
make clean   — remove compiled binaries and generated output file
make help    — list available targets
```
