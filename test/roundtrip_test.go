// Package roundtrip contains end-to-end integration tests that exercise the real
// compiled server, reader, and writer binaries and assert byte-perfect copies.
package roundtrip

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	buildOnce sync.Once
	binDir    string
	buildErr  error
)

// binaries compiles server, reader, and writer once into a temp dir shared by
// all tests, and returns their paths.
func binaries(t *testing.T) (server, reader, writer string) {
	t.Helper()
	buildOnce.Do(func() {
		binDir, buildErr = os.MkdirTemp("", "filequeue-bin")
		if buildErr != nil {
			return
		}
		for _, cmd := range []string{"server", "reader", "writer"} {
			out := filepath.Join(binDir, cmd)
			build := exec.Command("go", "build", "-o", out, "./cmd/"+cmd)
			build.Dir = repoRoot(t)
			if output, err := build.CombinedOutput(); err != nil {
				buildErr = &buildFailure{cmd: cmd, output: string(output), err: err}
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatalf("build binaries: %v", buildErr)
	}
	return filepath.Join(binDir, "server"),
		filepath.Join(binDir, "reader"),
		filepath.Join(binDir, "writer")
}

type buildFailure struct {
	cmd    string
	output string
	err    error
}

func (b *buildFailure) Error() string {
	return "build " + b.cmd + ": " + b.err.Error() + "\n" + b.output
}

// repoRoot walks up from the test file until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// freeAddr returns a currently-free loopback address.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

// waitDial blocks until addr accepts connections or the deadline passes.
func waitDial(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server at %s never became ready", addr)
}

// runPipeline copies inPath to outPath through a fresh broker and returns when
// the writer has finished.
func runPipeline(t *testing.T, inPath, outPath string) {
	t.Helper()
	serverBin, readerBin, writerBin := binaries(t)
	addr := freeAddr(t)

	srv := exec.Command(serverBin, "-addr", addr)
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
	}()
	waitDial(t, addr)

	wr := exec.Command(writerBin, "-addr", addr, "-out", outPath)
	wr.Stderr = os.Stderr
	if err := wr.Start(); err != nil {
		t.Fatalf("start writer: %v", err)
	}

	rd := exec.Command(readerBin, "-addr", addr, "-in", inPath)
	rd.Stderr = os.Stderr
	if err := rd.Run(); err != nil {
		t.Fatalf("reader: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- wr.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("writer: %v", err)
		}
	case <-time.After(10 * time.Second):
		_ = wr.Process.Kill()
		t.Fatal("writer did not finish within timeout")
	}
}

func sha(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return sha256.Sum256(b)
}

func TestRoundTripFidelity(t *testing.T) {
	cases := map[string][]byte{
		"empty":             {},
		"no trailing nl":    []byte("alpha\nbravo\ncharlie"),
		"trailing nl":       []byte("alpha\nbravo\ncharlie\n"),
		"crlf terminators":  []byte("alpha\r\nbravo\r\n"),
		"blank lines":       []byte("a\n\n\n\nb\n"),
		"long line > 64KiB": append(bytes.Repeat([]byte("A"), 70000), '\n'),
		"binary-ish bytes":  {0x00, 0x01, 0x0a, 0x0d, 0xff, 0x00, 'x', '\n', 0xfe},
		"only newlines":     []byte("\n\n\n"),
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			in := filepath.Join(dir, "in.txt")
			out := filepath.Join(dir, "out.txt")
			if err := os.WriteFile(in, content, 0o644); err != nil {
				t.Fatalf("write input: %v", err)
			}

			runPipeline(t, in, out)

			if got, want := sha(t, out), sha(t, in); got != want {
				t.Errorf("checksum mismatch for %q\n in=%x\nout=%x", name, want, got)
			}
		})
	}
}

func TestServerShutsDownOnSignal(t *testing.T) {
	serverBin, _, _ := binaries(t)
	addr := freeAddr(t)
	srv := exec.Command(serverBin, "-addr", addr)
	var stderr strings.Builder
	srv.Stderr = &stderr
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	waitDial(t, addr)

	if err := srv.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server exited with error: %v\n%s", err, stderr.String())
		}
	case <-time.After(3 * time.Second):
		_ = srv.Process.Kill()
		t.Fatal("server did not exit on signal")
	}
}

// TestServerClosesIdleConnection verifies the rolling idle deadline drops a
// connection that declares a role then stalls mid-frame (a slowloris client),
// rather than holding a goroutine and queue slot indefinitely.
func TestServerClosesIdleConnection(t *testing.T) {
	serverBin, _, _ := binaries(t)
	addr := freeAddr(t)
	srv := exec.Command(serverBin, "-addr", addr, "-idle", "300ms")
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
	}()
	waitDial(t, addr)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Declare producer role, then send only a partial frame header and stall.
	if _, err := conn.Write([]byte{'P', 0x00, 0x00}); err != nil {
		t.Fatalf("write partial frame: %v", err)
	}

	// The server must drop the idle connection well before our own deadline.
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	buf := make([]byte, 1)
	switch _, err := conn.Read(buf); {
	case err == nil:
		t.Fatal("expected server to close idle connection, got data")
	case errors.Is(err, io.EOF):
		// success: server closed the connection at the idle deadline
	case os.IsTimeout(err):
		t.Fatal("server did not close idle connection within timeout")
	default:
		// Any other read error (e.g. connection reset) also means the server
		// closed the connection, which is the behaviour under test.
	}
}

func TestMultiStreamIsolation(t *testing.T) {
	serverBin, readerBin, writerBin := binaries(t)
	addr := freeAddr(t)
	srv := exec.Command(serverBin, "-addr", addr)
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
	}()
	waitDial(t, addr)

	dir := t.TempDir()
	type stream struct {
		id      string
		content []byte
	}
	streams := []stream{
		{"alpha", bytes.Repeat([]byte("A"), 200000)},
		{"bravo", []byte("bravo\nlines\nhere\n")},
		{"charlie", append(bytes.Repeat([]byte("C"), 70000), '\n')},
		{"delta", nil}, // empty file must round-trip as empty
	}

	var wg sync.WaitGroup
	for _, s := range streams {
		in := filepath.Join(dir, s.id+".in")
		out := filepath.Join(dir, s.id+".out")
		if err := os.WriteFile(in, s.content, 0o644); err != nil {
			t.Fatalf("write input %s: %v", s.id, err)
		}
		wg.Add(1)
		go func(id, in, out string) {
			defer wg.Done()
			wr := exec.Command(writerBin, "-addr", addr, "-out", out, "-stream", id)
			wr.Stderr = os.Stderr
			if err := wr.Start(); err != nil {
				t.Errorf("start writer %s: %v", id, err)
				return
			}
			rd := exec.Command(readerBin, "-addr", addr, "-in", in, "-stream", id)
			rd.Stderr = os.Stderr
			if err := rd.Run(); err != nil {
				t.Errorf("reader %s: %v", id, err)
				return
			}
			if err := wr.Wait(); err != nil {
				t.Errorf("writer %s: %v", id, err)
			}
		}(s.id, in, out)
	}
	wg.Wait()

	for _, s := range streams {
		in := filepath.Join(dir, s.id+".in")
		out := filepath.Join(dir, s.id+".out")
		if got, want := sha(t, out), sha(t, in); got != want {
			t.Errorf("stream %s: output does not match input\n in=%x\nout=%x", s.id, want, got)
		}
	}
}

func TestConsumerAttachTimeout(t *testing.T) {
	serverBin, _, writerBin := binaries(t)
	addr := freeAddr(t)
	srv := exec.Command(serverBin, "-addr", addr, "-attach-timeout", "300ms")
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
	}()
	waitDial(t, addr)

	out := filepath.Join(t.TempDir(), "ghost.out")
	wr := exec.Command(writerBin, "-addr", addr, "-out", out, "-stream", "ghost")
	wr.Stderr = os.Stderr
	if err := wr.Start(); err != nil {
		t.Fatalf("start writer: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- wr.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("writer exited with error: %v", err)
		}
	case <-time.After(3 * time.Second):
		_ = wr.Process.Kill()
		t.Fatal("writer did not exit after the attach timeout")
	}
	if b, _ := os.ReadFile(out); len(b) != 0 {
		t.Errorf("expected empty output for absent stream, got %d bytes", len(b))
	}
}

// TestDuplicateProducerIsRejected verifies the attach acknowledgement: once a
// stream has an owner, a second producer on the same stream fails fast with a
// non-zero exit instead of silently dropping its input, and the first
// producer's data still round-trips to the consumer untouched.
func TestDuplicateProducerIsRejected(t *testing.T) {
	serverBin, readerBin, writerBin := binaries(t)
	addr := freeAddr(t)
	srv := exec.Command(serverBin, "-addr", addr)
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
	}()
	waitDial(t, addr)

	dir := t.TempDir()
	in := filepath.Join(dir, "first.in")
	payload := bytes.Repeat([]byte("first-producer-data\n"), 1000)
	if err := os.WriteFile(in, payload, 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	// First producer attaches, publishes its data, and exits. The stream is now
	// owned and its frames stay buffered until a consumer drains them.
	first := exec.Command(readerBin, "-addr", addr, "-in", in, "-stream", "dup")
	first.Stderr = os.Stderr
	if err := first.Run(); err != nil {
		t.Fatalf("first producer: %v", err)
	}

	// Second producer on the same stream must be rejected and exit non-zero.
	other := filepath.Join(dir, "second.in")
	if err := os.WriteFile(other, []byte("second-should-be-rejected\n"), 0o644); err != nil {
		t.Fatalf("write second input: %v", err)
	}
	second := exec.Command(readerBin, "-addr", addr, "-in", other, "-stream", "dup")
	second.Stderr = os.Stderr
	if err := second.Run(); err == nil {
		t.Fatal("second producer on a taken stream should fail, but it exited 0")
	}

	// The consumer drains the stream and must receive the first producer's data
	// untouched — the rejected producer contributed nothing.
	out := filepath.Join(dir, "dup.out")
	wr := exec.Command(writerBin, "-addr", addr, "-out", out, "-stream", "dup")
	wr.Stderr = os.Stderr
	if err := wr.Run(); err != nil {
		t.Fatalf("consumer: %v", err)
	}
	if got, want := sha(t, out), sha(t, in); got != want {
		t.Errorf("consumer output does not match the first producer's input")
	}
}

// TestDuplicateConsumerIsRejected verifies that a stream allows only one
// consumer. While the first consumer is attached and waiting for a producer, a
// second consumer on the same stream is rejected by the attach ack and exits
// non-zero instead of silently competing for the same frames.
func TestDuplicateConsumerIsRejected(t *testing.T) {
	serverBin, _, writerBin := binaries(t)
	addr := freeAddr(t)
	srv := exec.Command(serverBin, "-addr", addr, "-attach-timeout", "1s")
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
	}()
	waitDial(t, addr)

	dir := t.TempDir()
	out1 := filepath.Join(dir, "first.out")
	out2 := filepath.Join(dir, "second.out")

	// First consumer attaches and blocks waiting for a producer; it holds the
	// stream's single consumer slot for the duration of the attach timeout.
	first := exec.Command(writerBin, "-addr", addr, "-out", out1, "-stream", "solo")
	first.Stderr = os.Stderr
	if err := first.Start(); err != nil {
		t.Fatalf("start first consumer: %v", err)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- first.Wait() }()

	// Let the first consumer attach before the second one connects.
	time.Sleep(250 * time.Millisecond)

	// Second consumer on the same stream must be rejected and exit non-zero.
	second := exec.Command(writerBin, "-addr", addr, "-out", out2, "-stream", "solo")
	second.Stderr = os.Stderr
	if err := second.Run(); err == nil {
		t.Fatal("second consumer on a taken stream should fail, but it exited 0")
	}

	// The first consumer keeps its slot, then exits cleanly once its attach
	// timeout elapses with no producer, leaving an empty output file.
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first consumer exited with error: %v", err)
		}
	case <-time.After(3 * time.Second):
		_ = first.Process.Kill()
		t.Fatal("first consumer did not exit after its attach timeout")
	}
	if b, _ := os.ReadFile(out1); len(b) != 0 {
		t.Errorf("expected empty output for the timed-out consumer, got %d bytes", len(b))
	}
}
