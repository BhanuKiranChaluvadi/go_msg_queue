MODULE := medconnect
BIN    := bin

.PHONY: all build test race cover vet fmt check run demo clean help

all: build

## build — compile the server binary into ./bin/
build:
	@mkdir -p $(BIN)
	go build -o $(BIN)/server ./cmd/server
	@echo "built: $(BIN)/server"

## test — run all unit tests
test:
	go test ./...

## race — run all tests with the race detector
race:
	go test -race ./...

## cover — report total test coverage across all packages
cover:
	go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

## vet — run go vet static analysis
vet:
	go vet ./...

## fmt — fail if any file is not gofmt-clean
fmt:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)

## check — run every CI quality gate locally (fmt, vet, build, race)
check: fmt vet build race

## run — start the API hub on :8080 (single binary, workers embedded)
run: build
	./$(BIN)/server -addr :8080

## demo — run the end-to-end API walkthrough (needs a running server: make run)
demo:
	./scripts/demo.sh

## clean — remove compiled binaries and coverage output
clean:
	rm -rf $(BIN) coverage.out

## help — list available targets
help:
	@grep -E '^## ' Makefile
