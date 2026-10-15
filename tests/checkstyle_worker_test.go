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
	"-cp", "/opt/checkstyle.jar:/opt/checkstyle-worker.jar", "CheckstyleWorker",
	"--config", "/opt/checkstyle-config.xml", "--threads", "2",
}

// javaUnicode is linted under a non-ASCII file name, which checkstyle
// echoes into the XML, so the reply escaping is compared against the CLI
// byte for byte.
const javaUnicode = `public class Unicode {
    public static void main(String[] args) {
        String s = "héllo wörld ✓ 😀 ………………………………………………………………………………………………………………………………………………………………………………………………………………";
        if (s == "x") System.out.println(s);
    }
}
`

const javaBroken = "public class Broken {\n"

// TestCheckstyleWorkerMatchesCLI checks that the resident worker's reply
// is exactly the XML `java -jar checkstyle.jar -f xml` prints for the same
// files, and that a source checkstyle cannot parse is an error reply.
func TestCheckstyleWorkerMatchesCLI(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"Clean.java":     strings.ReplaceAll(javaClean, "Solution", "Clean"),
		"Dirty.java":     strings.ReplaceAll(javaDirty, "Solution", "Dirty"),
		"Ünïcødé ✓.java": javaUnicode,
		"Broken.java":    javaBroken,
	}
	mounts := []string{}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		mounts = append(mounts, "-v", path+":/workspace/"+name+":ro")
	}
	requests := [][]string{
		{"/workspace/Clean.java"},
		{"/workspace/Dirty.java"},
		{"/workspace/Ünïcødé ✓.java"},
		{"/workspace/Clean.java", "/workspace/Dirty.java"},
		{"/workspace/Broken.java"},
	}

	var stdin bytes.Buffer
	for i, req := range requests {
		line, _ := json.Marshal(map[string]any{"id": i + 1, "files": req})
		stdin.Write(append(line, '\n'))
	}
	// LANG matches the tool environment serve gives the worker (contract
	// section 9.3); Java derives its file name encoding from it.
	args := append([]string{"run", "--rm", "-i", "-e", "LANG=C.UTF-8", "-w", "/workspace"}, mounts...)
	args = append(args, "--entrypoint", "/opt/java/bin/java", image())
	args = append(args, workerArgv...)
	argv := append(append([]string{}, dockerCmd()...), args...)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = &stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("worker did not exit 0 on stdin EOF: %v\nstderr: %s", err, stderr.String())
	}

	type reply struct {
		ID    int     `json:"id"`
		XML   *string `json:"xml"`
		Error *string `json:"error"`
	}
	replies := map[int]reply{}
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(nil, 32<<20)
	for sc.Scan() {
		var r reply
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

	for i, req := range requests {
		r := replies[i+1]
		cliArgs := append([]string{"run", "--rm", "-e", "LANG=C.UTF-8", "-w", "/workspace"}, mounts...)
		cliArgs = append(cliArgs, "--entrypoint", "/opt/java/bin/java", image(),
			"-jar", "/opt/checkstyle.jar", "-c", "/opt/checkstyle-config.xml", "-f", "xml")
		cliArgs = append(cliArgs, req...)
		cliOut, cliErr, code := docker(t, cliArgs...)

		if strings.HasSuffix(req[0], "Broken.java") {
			if code == 0 || r.Error == nil || !strings.Contains(*r.Error, "mismatched input") {
				t.Errorf("%v: CLI exit %d, worker reply %+v; want a CLI failure and an error reply naming the parse error\nCLI stderr: %s", req, code, r, cliErr)
			}
			continue
		}
		if r.XML == nil {
			t.Errorf("%v: worker error reply %q", req, deref(r.Error))
			continue
		}
		if !strings.Contains(*r.XML, `name="`+req[len(req)-1]+`"`) {
			t.Errorf("%v: worker XML does not name the file:\n%s", req, *r.XML)
		}
		if *r.XML != cliOut {
			t.Errorf("%v: worker XML differs from the CLI\nworker:\n%s\nCLI (exit %d):\n%s", req, *r.XML, code, cliOut)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
