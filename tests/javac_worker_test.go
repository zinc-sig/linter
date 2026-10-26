//go:build conformance

package tests

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// workerArgv is the Appendix A launch line, run with the image's java as
// the entrypoint.
var workerArgv = []string{
	"-XX:+UseSerialGC", "-Xmx128m", "-XX:-UsePerfData",
	"-jar", "/opt/javac-worker.jar", "--threads", "4", "--warmup", "/opt/javac-warmup/Warmup.java",
}

// javaUnicode is linted under a non-ASCII file name and holds non-ASCII
// text before the error, so the reply escaping and the column count are
// compared against the CLI.
const javaUnicode = `public class Unicode {
    public static void main(String[] args) {
        String s = "héllo wörld ✓ 😀"; int n = s;
    }
}
`

const javaBroken = "public class Broken {\n"

type workerReply struct {
	ID     int             `json:"id"`
	Report json.RawMessage `json:"report"`
	Error  *string         `json:"error"`
}

// runWorker starts the resident worker over dir mounted at /workspace,
// writes one request per entry of requests and returns the replies by id.
func runWorker(t *testing.T, dir string, requests []map[string]any) map[int]workerReply {
	t.Helper()
	var stdin bytes.Buffer
	for i, req := range requests {
		req["id"] = i + 1
		line, _ := json.Marshal(req)
		stdin.Write(append(line, '\n'))
	}
	// LANG matches the tool environment serve gives the worker (contract
	// section 9.3); Java derives its file name encoding from it.
	args := []string{"run", "--rm", "-i", "-e", "LANG=C.UTF-8", "-v", dir + ":/workspace:ro",
		"--entrypoint", "/opt/java/bin/java", image()}
	args = append(args, workerArgv...)
	argv := append(append([]string{}, dockerCmd()...), args...)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = &stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("worker did not exit 0 on stdin EOF: %v\nstderr: %s", err, stderr.String())
	}
	replies := map[int]workerReply{}
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(nil, 32<<20)
	for sc.Scan() {
		var r workerReply
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("stdout line is not a reply: %q", sc.Text())
		}
		if _, dup := replies[r.ID]; dup {
			t.Fatalf("two replies for id %d", r.ID)
		}
		replies[r.ID] = r
	}
	if len(replies) != len(requests) {
		t.Fatalf("got %d replies, want %d\nstderr: %s", len(replies), len(requests), stderr.String())
	}
	return replies
}

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func canonicalJSON(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// TestJavacWorkerMatchesCLI checks that the resident worker's report is
// the one `JavacWorker --once` prints for the same file in the same
// directory, apart from the path the file was named by.
func TestJavacWorkerMatchesCLI(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"Clean.java":     strings.ReplaceAll(javaClean, "Solution", "Clean"),
		"Dirty.java":     strings.ReplaceAll(javaDirty, "Solution", "Dirty"),
		"Ünïcødé ✓.java": javaUnicode,
		"Broken.java":    javaBroken,
	})
	names := []string{"Clean.java", "Dirty.java", "Ünïcødé ✓.java", "Broken.java"}
	var requests []map[string]any
	for _, name := range names {
		requests = append(requests, map[string]any{"release": "11", "dir": "/workspace", "files": []string{"/workspace/" + name}})
	}
	replies := runWorker(t, dir, requests)

	for i, name := range names {
		r := replies[i+1]
		if r.Error != nil {
			t.Errorf("%s: error reply %q", name, *r.Error)
			continue
		}
		cliOut, cliErr, code := docker(t, "run", "--rm", "-e", "LANG=C.UTF-8", "-v", dir+":/workspace:ro", "-w", "/workspace",
			"--entrypoint", "/opt/java/bin/java", image(), "-jar", "/opt/javac-worker.jar", "--once", "--release", "11", name)
		if code != 0 {
			t.Errorf("%s: CLI exit %d: %s", name, code, cliErr)
			continue
		}
		want := strings.TrimSpace(cliOut)
		got := strings.ReplaceAll(string(r.Report), `"path":"/workspace/`, `"path":"`)
		if canonicalJSON(t, got) != canonicalJSON(t, want) {
			t.Errorf("%s: worker report differs from the CLI\nworker: %s\nCLI:    %s", name, r.Report, want)
		}
		if name != "Clean.java" && !strings.Contains(want, `"severity":"error"`) {
			t.Errorf("%s: expected an error finding: %s", name, want)
		}
	}
}

// TestJavacWorkerIsolatesWorkspaces sends interleaved requests over
// several workspaces to one worker running four threads, so each thread's
// reused compiler sees the workspaces in many orders. A class that one
// workspace provides must resolve there and nowhere else, a duplicate of
// the student class in a sibling is not reported, and the directories
// that hold no program sources are not compiled.
func TestJavacWorkerIsolatesWorkspaces(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"A/Helper.java":   "class Helper { static int a() { return 1; } }\n",
		"A/Solution.java": "public class Solution { int f() { return Helper.a(); } }\n",
		"B/Helper.java":   "class Helper { static int b() { return 2; } }\n",
		"B/Solution.java": "public class Solution { int f() { return Helper.b(); } }\n",
		"C/Solution.java": "public class Solution { int f() { return Helper.a(); } }\n",
		"P/src/app/Main.java": "package app; public class Main { public static void main(String[] a) { " +
			"System.out.println(new Solution().f()); } }\n",
		"P/src/app/Solution.java":     "package app; public class Solution { public int f() { return Util.k(); } }\n",
		"P/src/app/Util.java":         "package app; class Util { static int k() { return 3; } }\n",
		"P/ref/app/Solution.java":     "package app; public class Solution { }\n",
		"P/.zinc/app/Util.java":       "package app; class Util { }\n",
		"Q/src/app/Solution.java":     "package app; public class Solution { public int f() { return Util.k(); } }\n",
		"Q/examination-assets/U.java": "package app; class Util { static int k() { return 3; } }\n",
	})
	cases := []struct {
		ws, file string
		wantErr  bool
	}{
		{"A", "Solution.java", false},
		{"B", "Solution.java", false},
		{"C", "Solution.java", true},
		{"P", "src/app/Solution.java", false},
		{"Q", "src/app/Solution.java", true},
	}
	var requests []map[string]any
	var want []bool
	for round := range 12 {
		for i := range cases {
			c := cases[(i+round)%len(cases)]
			requests = append(requests, map[string]any{
				"release": "11", "dir": "/workspace/" + c.ws, "files": []string{"/workspace/" + c.ws + "/" + c.file},
			})
			want = append(want, c.wantErr)
		}
	}
	replies := runWorker(t, dir, requests)
	for i, req := range requests {
		r := replies[i+1]
		if r.Error != nil {
			t.Errorf("request %d %v: error reply %q", i+1, req["files"], *r.Error)
			continue
		}
		hasErr := strings.Contains(string(r.Report), "compiler.err.cant.resolve")
		if hasErr != want[i] || strings.Contains(string(r.Report), "duplicate") {
			t.Errorf("request %d %v: report %s, want an unresolved symbol: %v", i+1, req["files"], r.Report, want[i])
		}
	}
}

// TestJavacWorkerBadRequests checks that an unsupported release and a
// missing file fail their request with an error reply, not the worker.
func TestJavacWorkerBadRequests(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"A.java": "class A {}\n"})
	replies := runWorker(t, dir, []map[string]any{
		{"release": "99", "dir": "/workspace", "files": []string{"/workspace/A.java"}},
		{"release": "11", "dir": "/workspace", "files": []string{"/workspace/Missing.java"}},
		{"release": "11", "dir": "/workspace", "files": []string{"/workspace/A.java"}},
	})
	for id, want := range map[int]string{1: "release 99 is not supported", 2: "Missing.java"} {
		if r := replies[id]; r.Error == nil || !strings.Contains(*r.Error, want) {
			t.Errorf("request %d: reply %+v, want an error naming %q", id, r, want)
		}
	}
	if r := replies[3]; r.Error != nil || !strings.Contains(string(r.Report), `"findings":[]`) {
		t.Errorf("request 3 after the failures: %s %v", r.Report, r.Error)
	}
}
