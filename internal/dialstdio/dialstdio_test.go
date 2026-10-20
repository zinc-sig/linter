package dialstdio

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cld")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func listen(t *testing.T, path string) net.Listener {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

// syncWriter collects stdout safely across goroutines.
type syncWriter struct {
	ch chan []byte
}

func (w syncWriter) Write(p []byte) (int, error) {
	w.ch <- append([]byte(nil), p...)
	return len(p), nil
}

// TestHalfClose: stdin EOF half-closes the socket, and the reply still
// reaches stdout afterwards.
func TestHalfClose(t *testing.T) {
	path := socketPath(t)
	ln := listen(t, path)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		got, _ := io.ReadAll(c) // returns at the client's half-close
		c.Write([]byte("echo:" + string(got)))
	}()
	var stdout, stderr bytes.Buffer
	code := Run(path, strings.NewReader("ping"), &stdout, &stderr)
	if code != 0 || stdout.String() != "echo:ping" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

// TestSocketEOFDoesNotWaitForStdin: the server closing the connection
// ends the bridge even though stdin stays open.
func TestSocketEOFDoesNotWaitForStdin(t *testing.T) {
	path := socketPath(t)
	ln := listen(t, path)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 4)
		io.ReadFull(c, buf)
		c.Write([]byte("bye:" + string(buf)))
		c.Close()
	}()
	stdinR, stdinW := io.Pipe()
	defer stdinW.Close()
	go stdinW.Write([]byte("abcd"))

	out := syncWriter{ch: make(chan []byte, 16)}
	done := make(chan int, 1)
	go func() { done <- Run(path, stdinR, out, io.Discard) }()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dial-stdio waited for stdin after the socket closed")
	}
	close(out.ch)
	var got []byte
	for b := range out.ch {
		got = append(got, b...)
	}
	if string(got) != "bye:abcd" {
		t.Errorf("stdout = %q", got)
	}
}

func TestConnectFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(filepath.Join(t.TempDir(), "missing.sock"), strings.NewReader("x"), &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "cobe-lint: dial-stdio: ") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestUsage(t *testing.T) {
	if code := Main([]string{"--bogus"}, io.Discard); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if code := Main([]string{"extra"}, io.Discard); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}
