//go:build conformance

package tests

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	serveSocket = "/tmp/cobe-lint.sock"
	tokenHeader = "X-Zinc-Linter-Token"
	serveToken  = "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00"
)

// containerArgs are the docker run flags core uses for the linter
// container on Docker, minus the network mode.
func containerArgs() []string {
	return []string{
		"--cpus", "2", "--memory", "4g", "--pids-limit", "256",
		"--read-only", "--cap-drop", "ALL",
		"--tmpfs", "/workspace:rw,noexec,nosuid,size=256m,mode=1777",
		"--tmpfs", "/tmp:rw,noexec,nosuid,size=512m,mode=1777",
	}
}

// startServe runs the image's server in a detached container named after
// the test and removes it at cleanup. extra goes before the image, serve
// flags after the serve argv core passes.
func startServe(t *testing.T, extra []string, serveFlags ...string) string {
	t.Helper()
	name := fmt.Sprintf("cobe-lint-conf-%d", time.Now().UnixNano())
	args := append([]string{"run", "-d", "--name", name}, containerArgs()...)
	args = append(args, extra...)
	args = append(args, image(), cliPath, "serve", "--socket", serveSocket, "--workers", "4")
	args = append(args, serveFlags...)
	if _, stderr, code := docker(t, args...); code != 0 {
		t.Fatalf("docker run: exit %d: %s", code, stderr)
	}
	t.Cleanup(func() {
		if t.Failed() {
			logs, errLogs, _ := docker(t, "logs", "--tail", "40", name)
			t.Logf("container log:\n%s%s", logs, errLogs)
		}
		docker(t, "rm", "-f", name)
	})
	return name
}

// execConn is one connection to the server through `docker exec -i ...
// dial-stdio`, the way core connects on Docker.
type execConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func dialExec(container string) (net.Conn, error) {
	argv := append(append([]string{}, dockerCmd()...), "exec", "-i", container, cliPath, "dial-stdio", "--socket", serveSocket)
	cmd := exec.Command(argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execConn{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

func (c *execConn) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *execConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }
func (c *execConn) CloseWrite() error           { return c.stdin.Close() }
func (c *execConn) Close() error {
	c.stdin.Close()
	_ = c.cmd.Process.Kill()
	_ = c.cmd.Wait()
	return nil
}
func (c *execConn) LocalAddr() net.Addr              { return execAddr{} }
func (c *execConn) RemoteAddr() net.Addr             { return execAddr{} }
func (c *execConn) SetDeadline(time.Time) error      { return nil }
func (c *execConn) SetReadDeadline(time.Time) error  { return nil }
func (c *execConn) SetWriteDeadline(time.Time) error { return nil }

type execAddr struct{}

func (execAddr) Network() string { return "exec" }
func (execAddr) String() string  { return "dial-stdio" }

// execClient pools dial-stdio connections like core's client.
func execClient(container string, conns int) *http.Client {
	tr := &http.Transport{
		DialContext:         func(context.Context, string, string) (net.Conn, error) { return dialExec(container) },
		MaxIdleConnsPerHost: conns,
		IdleConnTimeout:     90 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}
}

type served struct {
	client *http.Client
	base   string
	token  string
}

func (s served) do(t *testing.T, method, path string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, s.base+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if s.token != "" {
		req.Header.Set(tokenHeader, s.token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}

func (s served) lint(t *testing.T, req map[string]any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(req)
	return s.do(t, http.MethodPost, "/v1/lint", b)
}

// waitReady polls readiness; a cold Go warm-up takes 10 s or more.
func (s served) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, s.base+"/healthz/ready", nil)
		if resp, err := s.client.Do(req); err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			if bytes.Contains(body, []byte(`"failed"`)) {
				t.Fatalf("server failed: %s", body)
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("server never became ready")
}

func serveViaExec(t *testing.T) served {
	t.Helper()
	name := startServe(t, []string{"--network", "none"})
	s := served{client: execClient(name, 4), base: "http://linter"}
	s.waitReady(t)
	return s
}

// serveViaTCP starts the server with the Kubernetes flags and reaches it
// on the container's bridge address, which is not published on the host.
func serveViaTCP(t *testing.T) (served, string) {
	t.Helper()
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte(serveToken+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o755)
	name := startServe(t, []string{"-v", tokenFile + ":/run/zinc/token:ro", "-e", "COBE_CONF_SECRET=s3cr3t-in-env"},
		"--listen", ":8080", "--token-file", "/run/zinc/token")
	ip, _, code := docker(t, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", name)
	ip = strings.TrimSpace(ip)
	if code != 0 || ip == "" {
		t.Fatalf("no container address: %q", ip)
	}
	s := served{client: &http.Client{Timeout: 30 * time.Second}, base: "http://" + net.JoinHostPort(ip, "8080"), token: serveToken}
	s.waitReady(t)
	return s, name
}

// cliReport lints one fixture with the one-shot CLI for comparison.
func cliReport(t *testing.T, entry manifestEntry, lang, kind string) []byte {
	t.Helper()
	fixture, name := fixtureFile(t, lang, kind)
	stdout, stderr, code := lintFixture(t, entry, map[string]string{fixture: name}, name)
	if code != 0 {
		t.Fatalf("cobe-lint lint: exit %d: %s", code, stderr)
	}
	return canonical(t, []byte(stdout))
}

func canonical(t *testing.T, b []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("not JSON: %v: %s", err, b)
	}
	out, _ := json.Marshal(v)
	return out
}

// checkServeManifest compares GET /v1/manifest with `cobe-lint manifest`.
func checkServeManifest(t *testing.T, s served) manifestDoc {
	t.Helper()
	status, body := s.do(t, http.MethodGet, "/v1/manifest", nil)
	if status != 200 {
		t.Fatalf("manifest: %d %s", status, body)
	}
	var doc struct {
		manifestDoc
		Serve map[string]int `json:"serve"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Serve["protocol"] != 1 || doc.Serve["workers"] != 4 || doc.Serve["deadline_ms"] != 8000 {
		t.Errorf("serve object = %v", doc.Serve)
	}
	cli := loadManifest(t)
	a, _ := json.Marshal(cli)
	b, _ := json.Marshal(doc.manifestDoc)
	if !bytes.Equal(a, b) {
		t.Errorf("served manifest differs from cobe-lint manifest:\n%s\n%s", b, a)
	}
	return doc.manifestDoc
}

// lintEveryLanguage lints each manifest language's clean and dirty
// fixtures through s and expects exactly the CLI's report.
func lintEveryLanguage(t *testing.T, s served, m manifestDoc) {
	for lang, entry := range m.Languages {
		for _, kind := range []string{"clean", "dirty"} {
			t.Run(lang+"/"+kind, func(t *testing.T) {
				f := fixtures[lang][kind]
				status, body := s.lint(t, map[string]any{"language": lang, "filename": f.name, "code": f.content})
				if status != 200 {
					t.Fatalf("status %d: %s", status, body)
				}
				r := checkReport(t, string(body), lang)
				if kind == "dirty" && len(r.Findings) == 0 {
					t.Error("dirty sample produced no findings")
				}
				if kind == "clean" && len(r.Findings) != 0 {
					t.Errorf("clean sample produced findings: %v", r.Findings)
				}
				if got, want := canonical(t, body), cliReport(t, entry, lang, kind); !bytes.Equal(got, want) {
					t.Errorf("served report differs from the CLI's:\nserve: %s\ncli:   %s", got, want)
				}
			})
		}
	}
}

func TestServeThroughDialStdio(t *testing.T) {
	s := serveViaExec(t)
	m := checkServeManifest(t, s)
	lintEveryLanguage(t, s, m)

	t.Run("errors", func(t *testing.T) {
		cases := []struct {
			req  map[string]any
			code string
		}{
			{map[string]any{"language": "klingon", "filename": "a.k", "code": ""}, "unsupported_language"},
			{map[string]any{"language": "python313", "filename": "../a.py", "code": ""}, "filename_invalid"},
		}
		for _, tc := range cases {
			_, body := s.lint(t, tc.req)
			if !bytes.Contains(body, []byte(`"code":"`+tc.code+`"`)) {
				t.Errorf("%v: %s", tc.req, body)
			}
		}
	})
}

func TestServeAssets(t *testing.T) {
	s := serveViaExec(t)
	const key = "question-1"
	fp := strings.Repeat("ab", 32)
	req := map[string]any{
		"language": "c", "filename": "solution.c",
		"code":   "#include \"helper.h\"\nint main(void) { return helper(); }\n",
		"assets": map[string]string{"cache_key": key, "fingerprint": fp},
	}
	status, body := s.lint(t, req)
	if status != 409 || !bytes.Contains(body, []byte("assets_required")) {
		t.Fatalf("before upload: %d %s", status, body)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := "int helper(void) { return 0; }\n"
	tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "helper.h", Mode: 0o644, Size: int64(len(content))})
	tw.Write([]byte(content))
	tw.Close()
	if status, body := s.do(t, http.MethodPut, "/v1/assets/"+key+"/"+fp, buf.Bytes()); status != 204 {
		t.Fatalf("upload: %d %s", status, body)
	}
	status, body = s.lint(t, req)
	if status != 200 || bytes.Contains(body, []byte("not found")) {
		t.Errorf("with assets: %d %s", status, body)
	}
	delete(req, "assets")
	if _, body := s.lint(t, req); !bytes.Contains(body, []byte("'helper.h' file not found")) {
		t.Errorf("without assets the header must be missing: %s", body)
	}
}

// TestServeJavaWorkspace lints Java through the resident javac worker: a
// syntax error is a finding, the provided sources of the asset set
// resolve, and nothing one lint compiled is visible to the next.
func TestServeJavaWorkspace(t *testing.T) {
	s := serveViaExec(t)
	status, body := s.lint(t, map[string]any{"language": "java", "filename": "Broken.java", "code": "public class Broken {\n"})
	if status != 200 || !bytes.Contains(body, []byte("compiler.err.premature.eof")) {
		t.Errorf("syntax error: %d %s", status, body)
	}

	upload := func(key, fp string, files map[string]string) {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for name, content := range files {
			tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: int64(len(content))})
			tw.Write([]byte(content))
		}
		tw.Close()
		if status, body := s.do(t, http.MethodPut, "/v1/assets/"+key+"/"+fp, buf.Bytes()); status != 204 {
			t.Fatalf("upload %s: %d %s", key, status, body)
		}
	}
	fpA, fpB := strings.Repeat("aa", 32), strings.Repeat("bb", 32)
	upload("java-a", fpA, map[string]string{
		"src/app/Main.java": "package app;\npublic class Main {\n    public static void main(String[] a) {\n        System.out.println(new Solution().f());\n    }\n}\n",
		"src/app/Util.java": "package app;\nclass Util {\n    static int k() {\n        return 3;\n    }\n}\n",
		// A copy of the student class elsewhere is the sibling's duplicate.
		"reference/app/Solution.java": "package app;\npublic class Solution {\n    public int f() {\n        return 0;\n    }\n}\n",
	})
	upload("java-b", fpB, map[string]string{
		"src/app/Main.java": "package app;\npublic class Main {\n}\n",
	})
	code := "package app;\npublic class Solution {\n    public int f() {\n        return Util.k();\n    }\n}\n"
	lint := func(key, fp string) []byte {
		req := map[string]any{"language": "java", "filename": "src/app/Solution.java", "code": code}
		if key != "" {
			req["assets"] = map[string]string{"cache_key": key, "fingerprint": fp}
		}
		status, body := s.lint(t, req)
		if status != 200 {
			t.Fatalf("lint with %q: %d %s", key, status, body)
		}
		return body
	}
	for range 3 {
		if body := lint("java-a", fpA); bytes.Contains(body, []byte(`"severity"`)) {
			t.Errorf("Util is a provided source, so nothing is wrong: %s", body)
		}
		if body := lint("java-b", fpB); !bytes.Contains(body, []byte("compiler.err.cant.resolve")) {
			t.Errorf("Util from the other asset set must not resolve: %s", body)
		}
		if body := lint("", ""); !bytes.Contains(body, []byte("compiler.err.cant.resolve")) {
			t.Errorf("Util from an earlier lint must not resolve without assets: %s", body)
		}
	}
}

func TestServeOverTCPWithToken(t *testing.T) {
	s, _ := serveViaTCP(t)
	m := checkServeManifest(t, s)
	lintEveryLanguage(t, s, m)

	anon := served{client: s.client, base: s.base}
	if status, _ := anon.do(t, http.MethodGet, "/v1/manifest", nil); status != 401 {
		t.Errorf("manifest without token: %d", status)
	}
	if status, _ := anon.lint(t, map[string]any{"language": "c", "filename": "a.c", "code": ""}); status != 401 {
		t.Errorf("lint without token: %d", status)
	}
	for _, p := range []string{"/healthz/live", "/healthz/ready"} {
		if status, body := anon.do(t, http.MethodGet, p, nil); status != 200 {
			t.Errorf("%s without token: %d %s", p, status, body)
		}
	}

	// A student source can include files the linter user can read. The
	// token must never come back, and the container's environment never
	// reaches the tool's own environment.
	status, body := s.lint(t, map[string]any{"language": "c", "filename": "leak.c", "code": "#include \"/run/zinc/token\"\n"})
	if bytes.Contains(body, []byte(serveToken)) || !bytes.Contains(body, []byte("[redacted]")) {
		t.Errorf("the token file was not redacted (status %d): %s", status, body)
	}
	status, body = s.lint(t, map[string]any{"language": "c", "filename": "leak.c", "code": "#include \"/proc/self/environ\"\n"})
	if bytes.Contains(body, []byte("s3cr3t-in-env")) || bytes.Contains(body, []byte("/run/zinc/token")) {
		t.Errorf("the tool's environment carries the container's (status %d): %s", status, body)
	}
}
