package serve

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zinc-sig/linter/internal/checkstyleworker"
	"github.com/zinc-sig/linter/languages"
	"github.com/zinc-sig/linter/linter"
)

// fakeLinter runs script under sh with the files as $1...; each non-empty
// stdout line becomes a finding on the file. Exit status 42 is an
// operational failure.
type fakeLinter struct {
	lang   string
	script string
}

func (f fakeLinter) Language() string { return f.lang }
func (f fakeLinter) Name() string     { return "Fake " + f.lang }
func (f fakeLinter) Command(files []string) []string {
	return append([]string{"/bin/sh", "-c", f.script, "sh"}, files...)
}

func (f fakeLinter) Parse(stdout, stderr []byte, exitCode int) (linter.Report, error) {
	if exitCode == 42 {
		return linter.Report{}, errors.New("fake: " + linter.StderrSnippet(stderr))
	}
	r := linter.Report{Version: 1, Language: f.lang, Tool: "fake 1", ToolID: "fake", Findings: []linter.Finding{}}
	for _, line := range strings.Split(string(stdout), "\n") {
		if line == "" {
			continue
		}
		path := "reported"
		if p, msg, ok := strings.Cut(line, "|"); ok {
			path, line = p, msg
		}
		r.Findings = append(r.Findings, linter.Finding{Path: path, Line: 1, Severity: "warning", Message: line})
	}
	return r, nil
}

// fakeChecker stands in for the resident checkstyle worker.
type fakeChecker struct {
	ready, failed chan struct{}
	err           error
	check         func(ctx context.Context, files []string) (checkstyleworker.Output, error)
	closeOnce     sync.Once
	closed        chan struct{}
}

func newFakeChecker() *fakeChecker {
	return &fakeChecker{ready: make(chan struct{}), failed: make(chan struct{}), closed: make(chan struct{})}
}

func (c *fakeChecker) Check(ctx context.Context, files []string) (checkstyleworker.Output, error) {
	return c.check(ctx, files)
}
func (c *fakeChecker) Ready() <-chan struct{}  { return c.ready }
func (c *fakeChecker) Failed() <-chan struct{} { return c.failed }
func (c *fakeChecker) Err() error              { return c.err }
func (c *fakeChecker) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

type testServer struct {
	*Server
	cfg    Config
	client *http.Client // over the unix socket
	logs   *syncBuffer
	stop   func()
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type serverOpts struct {
	linters  []linter.Linter
	toolOf   map[string]string
	checker  *fakeChecker
	goWarmup func(ctx context.Context, s *Server) error
	tune     func(*Config)
	noReady  bool // skip waiting for readiness
}

// shortTempDir keeps unix socket paths under the 108-byte limit.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cls")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func startServer(t *testing.T, o serverOpts) *testServer {
	t.Helper()
	dir := shortTempDir(t)
	cfg := Config{
		Socket:        filepath.Join(dir, "s.sock"),
		Workers:       2,
		Queue:         2,
		Deadline:      5 * time.Second,
		CacheDir:      filepath.Join(dir, "cache"),
		CacheMaxBytes: 1 << 20,
		WorkDir:       filepath.Join(dir, "work"),
	}
	if o.tune != nil {
		o.tune(&cfg)
	}
	if o.linters == nil {
		o.linters = []linter.Linter{fakeLinter{lang: "fake", script: `cat "$1"`}}
	}
	logs := &syncBuffer{}
	deps := Deps{
		Linters:         o.linters,
		ManifestVersion: 1,
		ToolOf:          func(k string) string { return o.toolOf[k] },
		StartChecker: func(int, func(string)) (Checker, error) {
			if o.checker == nil {
				t.Fatal("unexpected checkstyle worker start")
			}
			return o.checker, nil
		},
		GoWarmup: o.goWarmup,
		Log:      logs,
	}
	s, err := New(cfg, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after cancellation")
		}
	}
	t.Cleanup(stop)
	ts := &testServer{Server: s, cfg: cfg, client: unixClient(cfg.Socket), logs: logs, stop: stop}
	if !o.noReady {
		ts.waitReady(t)
	}
	return ts
}

func unixClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}
}

func (ts *testServer) waitReady(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ts.state.isReady() {
			return
		}
	}
	t.Fatal("server never became ready")
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) errorCode(t *testing.T) string {
	t.Helper()
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.body, &e); err != nil || e.Error.Code == "" || e.Error.Message == "" {
		t.Fatalf("not an error body (%v): %s", err, r.body)
	}
	return e.Error.Code
}

func (r response) report(t *testing.T) linter.Report {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", r.status, r.body)
	}
	var rep linter.Report
	if err := json.Unmarshal(r.body, &rep); err != nil {
		t.Fatalf("report: %v: %s", err, r.body)
	}
	return rep
}

func do(t *testing.T, c *http.Client, method, url string, body []byte, header http.Header) response {
	t.Helper()
	return doCtx(t, context.Background(), c, method, url, body, header)
}

func doCtx(t *testing.T, ctx context.Context, c *http.Client, method, url string, body []byte, header http.Header) response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: b}
}

func (ts *testServer) lint(t *testing.T, req map[string]any) response {
	t.Helper()
	b, _ := json.Marshal(req)
	return do(t, ts.client, http.MethodPost, "http://linter/v1/lint", b, nil)
}

func tarOf(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		content := []byte(h.Linkname)
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(content))
			h.Linkname = ""
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write(content); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// file is a regular tar entry; the content rides in Linkname for tarOf.
func file(name, content string) tar.Header {
	return tar.Header{Typeflag: tar.TypeReg, Name: name, Linkname: content}
}

func dir(name string) tar.Header {
	return tar.Header{Typeflag: tar.TypeDir, Name: name, Mode: 0o755}
}

const fp1 = "1111111111111111111111111111111111111111111111111111111111111111"
const fp2 = "2222222222222222222222222222222222222222222222222222222222222222"

var _ = languages.ToolCheckstyle
