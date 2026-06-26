// Package roundtrip contains end-to-end integration tests that exercise the real
// compiled server, reader, and writer binaries and assert byte-perfect copies.
package roundtrip

import (
	"bytes"
	"crypto/sha256"
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
