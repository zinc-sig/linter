package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zinc-sig/linter/internal/checkstyleworker"
	"github.com/zinc-sig/linter/languages"
	"github.com/zinc-sig/linter/linter"
)

func TestManifestEndpoint(t *testing.T) {
	ts := startServer(t, serverOpts{})
	r := do(t, ts.client, http.MethodGet, "http://linter/v1/manifest", nil, nil)
	if r.status != http.StatusOK {
		t.Fatalf("status = %d: %s", r.status, r.body)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	var m struct {
		Version   int `json:"version"`
		Languages map[string]struct {
			Name    string   `json:"name"`
			Command []string `json:"command"`
		} `json:"languages"`
		Serve map[string]int64 `json:"serve"`
	}
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatal(err)
	}
	if m.Version != 1 || m.Languages["fake"].Name != "Fake fake" {
		t.Errorf("manifest = %s", r.body)
	}
	want := map[string]int64{
		"protocol": 1, "workers": 2, "queue": 2, "deadline_ms": 5000,
		"max_code_bytes": MaxCodeBytes, "max_findings": MaxFindings, "cache_max_bytes": 1 << 20,
	}
	for k, v := range want {
		if m.Serve[k] != v {
			t.Errorf("serve.%s = %d, want %d", k, m.Serve[k], v)
		}
	}
}

func TestLintReturnsReport(t *testing.T) {
	ts := startServer(t, serverOpts{linters: []linter.Linter{
		fakeLinter{lang: "fake", script: `echo "$1|$(cat "$1")"`},
	}})
	r := ts.lint(t, map[string]any{"language": "fake", "filename": "src/app/solution.py", "code": "hello"})
	rep := r.report(t)
	if len(rep.Findings) != 1 || rep.Findings[0].Path != "src/app/solution.py" || rep.Findings[0].Message != "hello" {
		t.Errorf("findings = %+v", rep.Findings)
	}
	for _, h := range []string{"X-Zinc-Linter-Queue-Ms", "X-Zinc-Linter-Run-Ms"} {
		if _, err := strconv.Atoi(r.header.Get(h)); err != nil {
			t.Errorf("%s = %q", h, r.header.Get(h))
		}
	}
	entries, _ := os.ReadDir(ts.cfg.WorkDir)
	if len(entries) != 0 {
		t.Errorf("workspaces left behind: %v", entries)
	}
}

func TestLintValidation(t *testing.T) {
	ts := startServer(t, serverOpts{})
	cases := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"not json", `{`, 400, "bad_request"},
		{"trailing data", `{"language":"fake","filename":"a.py","code":""} {}`, 400, "bad_request"},
		{"missing language", `{"filename":"a.py","code":""}`, 400, "bad_request"},
		{"mistyped language", `{"language":5,"filename":"a.py","code":""}`, 400, "bad_request"},
		{"unknown language", `{"language":"klingon","filename":"a.py","code":""}`, 422, "unsupported_language"},
		{"missing filename", `{"language":"fake","code":""}`, 400, "bad_request"},
		{"empty filename", `{"language":"fake","filename":"","code":""}`, 422, "filename_invalid"},
		{"absolute filename", `{"language":"fake","filename":"/a.py","code":""}`, 422, "filename_invalid"},
		{"dash filename", `{"language":"fake","filename":"-a.py","code":""}`, 422, "filename_invalid"},
		{"dotdot filename", `{"language":"fake","filename":"a/../b.py","code":""}`, 422, "filename_invalid"},
		{"dotdot segment", `{"language":"fake","filename":"../b.py","code":""}`, 422, "filename_invalid"},
		{"unclean filename", `{"language":"fake","filename":"a//b.py","code":""}`, 422, "filename_invalid"},
		{"trailing slash", `{"language":"fake","filename":"a/","code":""}`, 422, "filename_invalid"},
		{"dot filename", `{"language":"fake","filename":".","code":""}`, 422, "filename_invalid"},
		{"backslash", `{"language":"fake","filename":"a\\b.py","code":""}`, 422, "filename_invalid"},
		{"control", `{"language":"fake","filename":"a\u0001.py","code":""}`, 422, "filename_invalid"},
		{"reserved zinc", `{"language":"fake","filename":".zinc/a.py","code":""}`, 422, "filename_invalid"},
		{"reserved assets", `{"language":"fake","filename":"examination-assets/a.py","code":""}`, 422, "filename_invalid"},
		{"long filename", `{"language":"fake","filename":"` + strings.Repeat("a", 256) + `","code":""}`, 422, "filename_invalid"},
		{"missing code", `{"language":"fake","filename":"a.py"}`, 400, "bad_request"},
		{"code too large", `{"language":"fake","filename":"a.py","code":"` + strings.Repeat("a", MaxCodeBytes+1) + `"}`, 413, "payload_too_large"},
		{"body too large", `{"language":"fake","filename":"a.py","code":"` + strings.Repeat("a", maxLintBody) + `"}`, 413, "payload_too_large"},
		{"assets without fingerprint", `{"language":"fake","filename":"a.py","code":"","assets":{"cache_key":"k"}}`, 400, "bad_request"},
		{"bad cache key", `{"language":"fake","filename":"a.py","code":"","assets":{"cache_key":"-k","fingerprint":"` + fp1 + `"}}`, 400, "bad_request"},
		{"bad fingerprint", `{"language":"fake","filename":"a.py","code":"","assets":{"cache_key":"k","fingerprint":"ABC"}}`, 400, "bad_request"},
		{"max_findings zero", `{"language":"fake","filename":"a.py","code":"","max_findings":0}`, 400, "bad_request"},
		{"max_findings too big", `{"language":"fake","filename":"a.py","code":"","max_findings":1001}`, 400, "bad_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := do(t, ts.client, http.MethodPost, "http://linter/v1/lint", []byte(tc.body), nil)
			if r.status != tc.status {
				t.Errorf("status = %d, want %d: %s", r.status, tc.status, r.body)
			}
			if code := r.errorCode(t); code != tc.code {
				t.Errorf("code = %q, want %q", code, tc.code)
			}
		})
	}
	// Valid edge cases.
	for _, body := range []string{
		`{"language":"fake","filename":"a.py","code":"","assets":null,"unknown":1}`,
		`{"language":"fake","filename":"` + strings.Repeat("a", 255) + `","code":"x"}`,
		`{"language":"fake","filename":"dir/sub/a.py","code":"x","max_findings":1000}`,
	} {
		if r := do(t, ts.client, http.MethodPost, "http://linter/v1/lint", []byte(body), nil); r.status != 200 {
			t.Errorf("%s: status = %d: %s", body, r.status, r.body)
		}
	}
}

func TestFindingsAndMessageCaps(t *testing.T) {
	ts := startServer(t, serverOpts{linters: []linter.Linter{
		fakeLinter{lang: "fake", script: `for i in 1 2 3 4 5; do echo "m$i"; done; printf 'é%.0s' $(seq 3000); echo`},
	}})
	rep := ts.lint(t, map[string]any{"language": "fake", "filename": "a.py", "code": "", "max_findings": 3}).report(t)
	if len(rep.Findings) != 3 || rep.Findings[2].Message != "m3" {
		t.Errorf("findings = %+v", rep.Findings)
	}
	rep = ts.lint(t, map[string]any{"language": "fake", "filename": "a.py", "code": ""}).report(t)
	long := rep.Findings[5].Message
	if len(long) != maxMessageBytes || !strings.HasPrefix(long, "é") {
		t.Errorf("long message has %d bytes, want %d of whole runes", len(long), maxMessageBytes)
	}
}

func TestOperationalFailure(t *testing.T) {
	ts := startServer(t, serverOpts{linters: []linter.Linter{
		fakeLinter{lang: "fake", script: `echo tool blew up >&2; exit 42`},
	}})
	r := ts.lint(t, map[string]any{"language": "fake", "filename": "a.py", "code": ""})
	if r.status != 500 || r.errorCode(t) != "linter_execution_failed" || !bytes.Contains(r.body, []byte("tool blew up")) {
		t.Errorf("got %d %s", r.status, r.body)
	}
}

func TestUnknownPathAndMethod(t *testing.T) {
	ts := startServer(t, serverOpts{})
	r := do(t, ts.client, http.MethodGet, "http://linter/v1/nope", nil, nil)
	if r.status != 404 || r.errorCode(t) != "not_found" {
		t.Errorf("unknown path: %d %s", r.status, r.body)
	}
	r = do(t, ts.client, http.MethodGet, "http://linter/v1/manifest/", nil, nil)
	if r.status != 404 {
		t.Errorf("trailing slash: %d", r.status)
	}
	r = do(t, ts.client, http.MethodGet, "http://linter/v1/lint", nil, nil)
	if r.status != 405 || r.errorCode(t) != "method_not_allowed" || r.header.Get("Allow") != "POST" {
		t.Errorf("wrong method: %d %v %s", r.status, r.header, r.body)
	}
}

func TestNotReadyUntilCheckstyleWarm(t *testing.T) {
	c := newFakeChecker()
	c.check = func(_ context.Context, files []string) (checkstyleworker.Output, error) {
		return checkstyleworker.Output{Stdout: []byte(files[0] + "|ok\n")}, nil
	}
	ts := startServer(t, serverOpts{
		linters: []linter.Linter{fakeLinter{lang: "java"}, fakeLinter{lang: "fake", script: "true"}},
		toolOf:  map[string]string{"java": languages.ToolCheckstyle},
		checker: c,
		noReady: true,
	})
	r := do(t, ts.client, http.MethodGet, "http://linter/healthz/ready", nil, nil)
	if r.status != 503 || !bytes.Contains(r.body, []byte(`"status":"starting"`)) || !bytes.Contains(r.body, []byte(`"pending":["checkstyle"]`)) {
		t.Errorf("ready before warm-up: %d %s", r.status, r.body)
	}
	r = ts.lint(t, map[string]any{"language": "fake", "filename": "a.py", "code": ""})
	if r.status != 503 || r.errorCode(t) != "linter_not_ready" || r.header.Get("Retry-After") != "1" {
		t.Errorf("lint before ready: %d %s", r.status, r.body)
	}
	if r := do(t, ts.client, http.MethodGet, "http://linter/v1/manifest", nil, nil); r.status != 200 {
		t.Errorf("manifest before ready: %d", r.status)
	}

	close(c.ready)
	ts.waitReady(t)
	r = do(t, ts.client, http.MethodGet, "http://linter/healthz/ready", nil, nil)
	if r.status != 200 || !bytes.Contains(r.body, []byte(`"status":"ready"`)) {
		t.Errorf("ready: %d %s", r.status, r.body)
	}
	rep := ts.lint(t, map[string]any{"language": "java", "filename": "src/app/Solution.java", "code": "class A {}"}).report(t)
	if len(rep.Findings) != 1 || rep.Findings[0].Path != "src/app/Solution.java" {
		t.Errorf("java findings = %+v", rep.Findings)
	}
}

func TestCheckstyleErrors(t *testing.T) {
	c := newFakeChecker()
	close(c.ready)
	var next error
	var out checkstyleworker.Output
	c.check = func(context.Context, []string) (checkstyleworker.Output, error) { return out, next }
	ts := startServer(t, serverOpts{
		linters: []linter.Linter{fakeLinter{lang: "java"}},
		toolOf:  map[string]string{"java": languages.ToolCheckstyle},
		checker: c,
	})
	cases := []struct {
		err    error
		out    checkstyleworker.Output
		status int
		code   string
	}{
		{checkstyleworker.ErrTimeout, checkstyleworker.Output{}, 504, "timeout"},
		{checkstyleworker.ErrRestarted, checkstyleworker.Output{}, 503, "linter_not_ready"},
		{checkstyleworker.ErrFailed, checkstyleworker.Output{}, 503, "linter_not_ready"},
		{nil, checkstyleworker.Output{Stderr: []byte("parse error"), ExitCode: 42}, 500, "linter_execution_failed"},
	}
	for _, tc := range cases {
		next, out = tc.err, tc.out
		r := ts.lint(t, map[string]any{"language": "java", "filename": "A.java", "code": ""})
		if r.status != tc.status || r.errorCode(t) != tc.code {
			t.Errorf("%v: got %d %s", tc.err, r.status, r.body)
		}
	}

	c.err = errors.New("three failed starts")
	close(c.failed)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if status, _, _ := ts.state.snapshot(); status == "failed" {
			break
		}
	}
	r := do(t, ts.client, http.MethodGet, "http://linter/healthz/ready", nil, nil)
	if r.status != 503 || !bytes.Contains(r.body, []byte(`"status":"failed"`)) || !bytes.Contains(r.body, []byte("three failed starts")) {
		t.Errorf("ready after failure: %d %s", r.status, r.body)
	}
}

func TestGoWarmupGatesReadiness(t *testing.T) {
	release := make(chan struct{})
	ts := startServer(t, serverOpts{
		linters: []linter.Linter{fakeLinter{lang: "go", script: "true"}},
		toolOf:  map[string]string{"go": languages.ToolGoVet},
		goWarmup: func(ctx context.Context, _ *Server) error {
			<-release
			return errors.New("warm-up failures do not block readiness")
		},
		noReady: true,
	})
	time.Sleep(50 * time.Millisecond)
	r := do(t, ts.client, http.MethodGet, "http://linter/healthz/ready", nil, nil)
	if r.status != 503 || !bytes.Contains(r.body, []byte(`"pending":["go-warmup"]`)) {
		t.Errorf("ready during warm-up: %d %s", r.status, r.body)
	}
	close(release)
	ts.waitReady(t)
}

func TestBusyAndQueue(t *testing.T) {
	dir := shortTempDir(t)
	gate := filepath.Join(dir, "go")
	script := `touch "` + dir + `/started.$$"; while [ ! -e "` + gate + `" ]; do sleep 0.01; done`
	ts := startServer(t, serverOpts{
		linters: []linter.Linter{fakeLinter{lang: "fake", script: script}},
		tune:    func(c *Config) { c.Workers, c.Queue = 1, 1 },
	})
	results := make(chan response, 2)
	for range 2 {
		go func() { results <- ts.lint(t, map[string]any{"language": "fake", "filename": "a.py", "code": ""}) }()
	}
	// Wait until one runs and the other is queued.
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		ts.pool.mu.Lock()
		admitted := ts.pool.admitted
		ts.pool.mu.Unlock()
		if admitted == 2 {
			break
		}
	}
	r := ts.lint(t, map[string]any{"language": "fake", "filename": "a.py", "code": ""})
	if r.status != 503 || r.errorCode(t) != "busy" || r.header.Get("Retry-After") != "1" {
		t.Errorf("third lint: %d %v %s", r.status, r.header, r.body)
	}
	// Health and manifest never queue.
	if r := do(t, ts.client, http.MethodGet, "http://linter/healthz/live", nil, nil); r.status != 200 {
		t.Errorf("live while busy: %d", r.status)
	}
	os.WriteFile(gate, nil, 0o644)
	for range 2 {
		if r := <-results; r.status != 200 {
			t.Errorf("admitted lint: %d %s", r.status, r.body)
		}
	}
}

func TestDeadlineKillsTool(t *testing.T) {
	ts := startServer(t, serverOpts{
		linters: []linter.Linter{fakeLinter{lang: "fake", script: "sleep 30"}},
		tune:    func(c *Config) { c.Deadline = 300 * time.Millisecond },
	})
	start := time.Now()
	r := ts.lint(t, map[string]any{"language": "fake", "filename": "a.py", "code": ""})
	if r.status != 504 || r.errorCode(t) != "timeout" {
		t.Errorf("got %d %s", r.status, r.body)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("timeout took %v", time.Since(start))
	}
}

func TestQueuedRequestTimesOut(t *testing.T) {
	ts := startServer(t, serverOpts{
		linters: []linter.Linter{fakeLinter{lang: "fake", script: "sleep 30"}},
		tune:    func(c *Config) { c.Workers, c.Queue, c.Deadline = 1, 1, 400*time.Millisecond },
	})
	results := make(chan response, 2)
	for range 2 {
		go func() { results <- ts.lint(t, map[string]any{"language": "fake", "filename": "a.py", "code": ""}) }()
	}
	for range 2 {
		if r := <-results; r.status != 504 || r.errorCode(t) != "timeout" {
			t.Errorf("got %d %s", r.status, r.body)
		}
	}
}

func TestClientDisconnectKillsTool(t *testing.T) {
	dir := shortTempDir(t)
	pidFile := filepath.Join(dir, "pid")
	ts := startServer(t, serverOpts{
		linters: []linter.Linter{fakeLinter{lang: "fake", script: `echo $$ > "` + pidFile + `"; exec sleep 30`}},
		tune:    func(c *Config) { c.Workers, c.Queue = 1, 0 },
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		b, _ := json.Marshal(map[string]any{"language": "fake", "filename": "a.py", "code": ""})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://linter/v1/lint", bytes.NewReader(b))
		if resp, err := ts.client.Do(req); err == nil {
			resp.Body.Close()
			t.Error("the cancelled request got a response")
		}
	}()
	var pid int
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && pid == 0; time.Sleep(10 * time.Millisecond) {
		b, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	if pid == 0 {
		t.Fatal("tool never started")
	}
	cancel()
	<-done
	gone := false
	for range 300 {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			gone = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !gone {
		t.Fatal("the tool survived the client's disconnect")
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ts.pool.longestHold() == 0 {
			break
		}
	}
	if ts.pool.longestHold() != 0 {
		t.Error("the worker slot was not released")
	}
	if !strings.Contains(ts.logs.String(), `"abandoned":true`) {
		t.Errorf("no abandoned request logged:\n%s", ts.logs.String())
	}
}

func TestAbandonedJavaHoldsSlotUntilReply(t *testing.T) {
	c := newFakeChecker()
	close(c.ready)
	reply := make(chan struct{})
	ctxErr := make(chan error, 1)
	c.check = func(ctx context.Context, files []string) (checkstyleworker.Output, error) {
		<-reply // ignores cancellation, as the real worker does once sent
		ctxErr <- ctx.Err()
		return checkstyleworker.Output{Stdout: []byte(files[0] + "|ok\n")}, nil
	}
	ts := startServer(t, serverOpts{
		linters: []linter.Linter{fakeLinter{lang: "java"}},
		toolOf:  map[string]string{"java": languages.ToolCheckstyle},
		checker: c,
		tune:    func(c *Config) { c.Workers, c.Queue = 1, 0 },
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		b, _ := json.Marshal(map[string]any{"language": "java", "filename": "A.java", "code": ""})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://linter/v1/lint", bytes.NewReader(b))
		if resp, err := ts.client.Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && ts.pool.longestHold() == 0; time.Sleep(10 * time.Millisecond) {
	}
	cancel()
	time.Sleep(100 * time.Millisecond)
	r := ts.lint(t, map[string]any{"language": "java", "filename": "A.java", "code": ""})
	if r.status != 503 || r.errorCode(t) != "busy" {
		t.Errorf("abandoned Java lint released its slot early: %d %s", r.status, r.body)
	}
	entries, _ := os.ReadDir(ts.cfg.WorkDir)
	if len(entries) != 1 {
		t.Errorf("workspace removed before the reply: %v", entries)
	}
	close(reply)
	if err := <-ctxErr; !errors.Is(err, context.Canceled) {
		t.Errorf("request context at the reply = %v, want context.Canceled", err)
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline) && ts.pool.longestHold() != 0; time.Sleep(10 * time.Millisecond) {
	}
	if entries, _ := os.ReadDir(ts.cfg.WorkDir); len(entries) != 0 {
		t.Errorf("workspace left after the reply: %v", entries)
	}
	// The slot is released before the handler returns and logs, so wait
	// for the log line that shows the late result was discarded.
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline) && !strings.Contains(ts.logs.String(), `"abandoned":true`); time.Sleep(10 * time.Millisecond) {
	}
	if !strings.Contains(ts.logs.String(), `"abandoned":true`) {
		t.Errorf("the late Java result was not discarded as abandoned:\n%s", ts.logs.String())
	}
}

func TestLivenessReportsStuckSlot(t *testing.T) {
	c := newFakeChecker()
	close(c.ready)
	unblock := make(chan struct{})
	c.check = func(context.Context, []string) (checkstyleworker.Output, error) {
		<-unblock
		return checkstyleworker.Output{}, checkstyleworker.ErrTimeout
	}
	ts := startServer(t, serverOpts{
		linters: []linter.Linter{fakeLinter{lang: "java"}},
		toolOf:  map[string]string{"java": languages.ToolCheckstyle},
		checker: c,
		tune:    func(c *Config) { c.Deadline = 100 * time.Millisecond },
	})
	defer close(unblock)
	go ts.lint(t, map[string]any{"language": "java", "filename": "A.java", "code": ""})
	time.Sleep(400 * time.Millisecond)
	r := do(t, ts.client, http.MethodGet, "http://linter/healthz/live", nil, nil)
	if r.status != 503 || !bytes.Contains(r.body, []byte(`"status":"stuck"`)) {
		t.Errorf("live: %d %s", r.status, r.body)
	}
}

func TestShutdownRemovesSocket(t *testing.T) {
	ts := startServer(t, serverOpts{})
	ts.stop()
	if _, err := os.Stat(ts.cfg.Socket); !os.IsNotExist(err) {
		t.Errorf("socket still present: %v", err)
	}
}

func TestStartupCleansDirectories(t *testing.T) {
	dir := shortTempDir(t)
	cache := filepath.Join(dir, "cache")
	work := filepath.Join(dir, "work")
	os.MkdirAll(filepath.Join(cache, "set-old"), 0o755)
	os.MkdirAll(filepath.Join(work, workspacePrefix+"old"), 0o755)
	os.MkdirAll(filepath.Join(work, "unrelated"), 0o755)
	ts := startServer(t, serverOpts{tune: func(c *Config) { c.CacheDir, c.WorkDir = cache, work }})
	_ = ts
	if _, err := os.Stat(filepath.Join(cache, "set-old")); !os.IsNotExist(err) {
		t.Error("stale cache entry survived startup")
	}
	if _, err := os.Stat(filepath.Join(work, workspacePrefix+"old")); !os.IsNotExist(err) {
		t.Error("stale workspace survived startup")
	}
	if _, err := os.Stat(filepath.Join(work, "unrelated")); err != nil {
		t.Error("startup removed an unrelated directory")
	}
}

func TestRequestLogLine(t *testing.T) {
	ts := startServer(t, serverOpts{})
	h := http.Header{"Traceparent": {"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}
	b, _ := json.Marshal(map[string]any{"language": "fake", "filename": "a.py", "code": "secret-code"})
	do(t, ts.client, http.MethodPost, "http://linter/v1/lint", b, h)
	logs := ts.logs.String()
	for _, want := range []string{`"path":"/v1/lint"`, `"status":200`, `"language":"fake"`, `"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"`, `"queue_ms"`, `"run_ms"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("log lacks %s:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "secret-code") {
		t.Error("the log contains the code")
	}
}
