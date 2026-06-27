MODULE      := filequeue
BINARY_DIR  := bin
SERVER_BIN  := $(BINARY_DIR)/server
READER_BIN  := $(BINARY_DIR)/reader
WRITER_BIN  := $(BINARY_DIR)/writer

INPUT_FILE   := test/testdata/sample.txt
OUTPUT_FILE  := test/testdata/output.txt
LARGE_FILE   := test/testdata/large.txt
LARGE_OUTPUT := test/testdata/large.out
SERVER_ADDR  := localhost:4000

.PHONY: all build run run-large test cover race vet fmt vuln check clean help

all: build

## build   — compile all three binaries into ./bin/
build:
	@mkdir -p $(BINARY_DIR)
	go build -o $(SERVER_BIN) ./cmd/server
	go build -o $(READER_BIN) ./cmd/reader
	go build -o $(WRITER_BIN) ./cmd/writer
	@echo "built: $(SERVER_BIN)  $(READER_BIN)  $(WRITER_BIN)"

## run     — start server + writer + reader, then verify output == input
run: build
	@mkdir -p $(dir $(OUTPUT_FILE))
	@set -e; \
	echo "→ starting queue server..."; \
	$(SERVER_BIN) -addr $(SERVER_ADDR) & SERVER_PID=$$!; \
	sleep 0.3; \
	echo "→ starting writer worker..."; \
	$(WRITER_BIN) -addr $(SERVER_ADDR) -out $(OUTPUT_FILE) & WRITER_PID=$$!; \
	sleep 0.1; \
	echo "→ starting reader worker..."; \
	$(READER_BIN) -addr $(SERVER_ADDR) -in $(INPUT_FILE); \
	wait $$WRITER_PID; \
	kill $$SERVER_PID 2>/dev/null || true; \
	echo "→ verifying output..."; \
	diff $(INPUT_FILE) $(OUTPUT_FILE) \
		&& echo "✓  output matches input" \
		|| (echo "✗  output differs from input" && exit 1)

## run-large — stream the ~11 MB large.txt through the pipeline and verify (generates it if missing)
run-large: $(LARGE_FILE)
	@$(MAKE) run INPUT_FILE=$(LARGE_FILE) OUTPUT_FILE=$(LARGE_OUTPUT)

$(LARGE_FILE):
	@mkdir -p $(dir $@)
	@yes "the quick brown fox jumps over the lazy dog 0123456789" | head -n 200000 > $@
	@echo "generated $@ ($$(wc -c <$@) bytes)"

## test    — run all unit tests
test:
	go test ./...

## cover   — report total test coverage across all packages
cover:
	go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

## race    — run all tests with the race detector (CI gold standard)
race:
	go test -race ./...

## vet     — run go vet static analysis
vet:
	go vet ./...

## fmt     — fail if any file is not gofmt-clean
fmt:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi

## vuln    — scan for known vulnerabilities (stdlib equivalent of npm audit)
vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

## check   — run every CI quality gate locally (fmt, vet, build, race, vuln)
check: fmt vet build race vuln
	@echo "✓  all quality gates passed"

## clean   — remove compiled binaries, coverage, and generated output file
clean:
	rm -rf $(BINARY_DIR) $(OUTPUT_FILE) $(LARGE_OUTPUT) coverage.out

## help    — list available targets
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
