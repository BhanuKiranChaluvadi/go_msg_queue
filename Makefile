MODULE      := filequeue
BINARY_DIR  := bin
SERVER_BIN  := $(BINARY_DIR)/server
READER_BIN  := $(BINARY_DIR)/reader
WRITER_BIN  := $(BINARY_DIR)/writer

INPUT_FILE  := test/testdata/sample.txt
OUTPUT_FILE := test/testdata/output.txt
SERVER_ADDR := localhost:4000

.PHONY: all build run test clean help

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

## test    — run all unit tests
test:
	go test ./...

## clean   — remove compiled binaries and generated output file
clean:
	rm -rf $(BINARY_DIR) $(OUTPUT_FILE)

## help    — list available targets
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
