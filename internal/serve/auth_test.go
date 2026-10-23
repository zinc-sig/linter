package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zinc-sig/linter/internal/javacworker"
	"github.com/zinc-sig/linter/languages"
	"github.com/zinc-sig/linter/linter"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func tcpServer(t *testing.T, script string) (*testServer, string, string) {
	t.Helper()
	tokenFile := filepath.Join(shortTempDir(t), "token")
	if err := os.WriteFile(tokenFile, []byte(testToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := startServer(t, serverOpts{
		linters: []linter.Linter{fakeLinter{lang: "fake", script: script}},
		tune: func(c *Config) {
			c.Listen = "127.0.0.1:0"
			c.TokenFile = tokenFile
		},
	})
	return ts, "http://" + ts.TCPAddr().String(), tokenFile
}

func TestTokenOnTCPOnly(t *testing.T) {
	ts, base, _ := tcpServer(t, "true")
	tcp := &http.Client{}
	good := http.Header{TokenHeader: {testToken}}
	bad := http.Header{TokenHeader: {strings.Repeat("x", len(testToken))}}

	cases := []struct {
		name   string
		method string
		path   string
		header http.Header
		status int
	}{
		{"manifest without token", "GET", "/v1/manifest", nil, 401},
		{"manifest with wrong token", "GET", "/v1/manifest", bad, 401},
		{"token in Authorization", "GET", "/v1/manifest", http.Header{"Authorization": {"Bearer " + testToken}}, 401},
		{"unknown path without token", "GET", "/v1/nope", nil, 401},
		{"lint without token", "POST", "/v1/lint", nil, 401},
		{"manifest with token", "GET", "/v1/manifest", good, 200},
		{"live without token", "GET", "/healthz/live", nil, 200},
		{"ready without token", "GET", "/healthz/ready", nil, 200},
		{"health POST without token", "POST", "/healthz/live", nil, 401},
	}
	for _, tc := range cases {
		r := do(t, tcp, tc.method, base+tc.path, nil, tc.header)
		if r.status != tc.status {
			t.Errorf("%s: status = %d, want %d: %s", tc.name, r.status, tc.status, r.body)
		}
		if tc.status == 401 && r.errorCode(t) != "unauthorized" {
			t.Errorf("%s: code = %s", tc.name, r.body)
		}
	}
	// The unix socket needs no token and ignores a wrong one.
	for _, h := range []http.Header{nil, bad} {
		if r := do(t, ts.client, "GET", "http://linter/v1/manifest", nil, h); r.status != 200 {
			t.Errorf("unix socket manifest: %d", r.status)
		}
	}
	b, _ := json.Marshal(map[string]any{"language": "fake", "filename": "a.py", "code": ""})
	if r := do(t, tcp, "POST", base+"/v1/lint", b, good); r.status != 200 {
		t.Errorf("TCP lint with token: %d %s", r.status, r.body)
	}
}

func TestShortTokenFailsStartup(t *testing.T) {
	dir := shortTempDir(t)
	tokenFile := filepath.Join(dir, "token")
	os.WriteFile(tokenFile, []byte(strings.Repeat("a", minTokenBytes-1)+"  \n"), 0o600)
	cfg := Config{Socket: filepath.Join(dir, "s.sock"), Listen: "127.0.0.1:0", TokenFile: tokenFile,
		Workers: 1, Deadline: DefaultDeadline, CacheDir: filepath.Join(dir, "c"), CacheMaxBytes: 1, WorkDir: filepath.Join(dir, "w")}
	if _, err := New(cfg, Deps{Log: &syncBuffer{}}); err == nil || !strings.Contains(err.Error(), "shorter") {
		t.Errorf("New with a short token: %v", err)
	}
}

// TestToolEnvironmentIsScrubbed runs a tool that prints its environment
// and checks that nothing of the server's reaches it.
func TestToolEnvironmentIsScrubbed(t *testing.T) {
	t.Setenv("COBE_TEST_SECRET", "hunter2-secret")
	ts, base, tokenFile := tcpServer(t, "env")
	b, _ := json.Marshal(map[string]any{"language": "fake", "filename": "a.py", "code": ""})
	for _, r := range []response{
		do(t, &http.Client{}, "POST", base+"/v1/lint", b, http.Header{TokenHeader: {testToken}}),
		do(t, ts.client, "POST", "http://linter/v1/lint", b, nil),
	} {
		rep := r.report(t)
		var env []string
		for _, f := range rep.Findings {
			env = append(env, f.Message)
		}
		all := strings.Join(env, "\n")
		for _, leak := range []string{"hunter2-secret", tokenFile, testToken, "[redacted]"} {
			if strings.Contains(all, leak) {
				t.Errorf("tool environment contains %q:\n%s", leak, all)
			}
		}
		for _, want := range []string{"HOME=/tmp", "TMPDIR=/tmp", "LANG=C.UTF-8"} {
			if !strings.Contains(all, want) {
				t.Errorf("tool environment lacks %s:\n%s", want, all)
			}
		}
	}
}

// TestTokenIsRedacted lets a tool read the token file, as a C include
// could, and checks that no response carries it.
func TestTokenIsRedacted(t *testing.T) {
	dir := shortTempDir(t)
	tokenFile := filepath.Join(dir, "token")
	os.WriteFile(tokenFile, []byte(testToken), 0o600)
	ts := startServer(t, serverOpts{
		linters: []linter.Linter{fakeLinter{lang: "fake", script: `echo "leak $(cat ` + tokenFile + `)"; echo "$(cat ` + tokenFile + `)" >&2; exit 42`}},
		tune:    func(c *Config) { c.Listen, c.TokenFile = "127.0.0.1:0", tokenFile },
	})
	b, _ := json.Marshal(map[string]any{"language": "fake", "filename": "a.py", "code": ""})
	for _, r := range []response{
		do(t, ts.client, "POST", "http://linter/v1/lint", b, nil),
		do(t, &http.Client{}, "POST", "http://"+ts.TCPAddr().String()+"/v1/lint", b, http.Header{TokenHeader: {testToken}}),
	} {
		if bytes.Contains(r.body, []byte(testToken)) || !bytes.Contains(r.body, []byte("[redacted]")) {
			t.Errorf("response not redacted: %s", r.body)
		}
	}
}

// TestTokenIsRedactedBeforeTruncation places the token across the finding
// message cap and the stderr snippet cap, where truncating first would
// leave a prefix the redaction no longer matches.
func TestTokenIsRedactedBeforeTruncation(t *testing.T) {
	dir := shortTempDir(t)
	tokenFile := filepath.Join(dir, "token")
	os.WriteFile(tokenFile, []byte(testToken), 0o600)
	pad := func(n int, c string) string { return strings.Repeat(c, n) }
	c := newFakeChecker()
	close(c.ready)
	var javaOut javacworker.Output
	c.check = func(context.Context, javacworker.Request) (javacworker.Output, error) { return javaOut, nil }
	ts := startServer(t, serverOpts{
		linters: []linter.Linter{
			fakeLinter{lang: "message", script: `printf '%s' ` + pad(4070, "a") + `; cat ` + tokenFile + `; echo`},
			fakeLinter{lang: "stderr", script: `{ printf '%s' ` + pad(1960, "b") + `; cat ` + tokenFile + `; } >&2; exit 42`},
			fakeLinter{lang: "java"},
		},
		toolOf:  map[string]string{"java": languages.ToolJavac},
		checker: c,
		tune:    func(c *Config) { c.Listen, c.TokenFile = "127.0.0.1:0", tokenFile },
	})
	cases := []struct {
		name, lang string
		javaOut    javacworker.Output
		status     int
	}{
		{"finding message", "message", javacworker.Output{}, 200},
		{"stderr snippet", "stderr", javacworker.Output{}, 500},
		{"java finding message", "java", javacworker.Output{Stdout: []byte(pad(4070, "a") + testToken + "\n")}, 200},
		{"java stderr snippet", "java", javacworker.Output{Stderr: []byte(pad(1960, "b") + testToken), ExitCode: 42}, 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			javaOut = tc.javaOut
			r := ts.lint(t, map[string]any{"language": tc.lang, "filename": "a.py", "code": ""})
			if r.status != tc.status || !bytes.Contains(r.body, []byte("[redacted]")) {
				t.Fatalf("got %d, want %d with the token redacted: %.200s", r.status, tc.status, r.body)
			}
			for i := 0; i+16 <= len(testToken); i++ {
				if part := testToken[i : i+16]; bytes.Contains(r.body, []byte(part)) {
					t.Fatalf("response carries %q of the token", part)
				}
			}
		})
	}
}
