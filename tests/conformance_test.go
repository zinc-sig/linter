//go:build conformance

// Package tests is the image conformance suite for docs/CONTRACT.md. It
// exercises the built image exactly the way core does — `docker run` with
// the workspace at /workspace and files appended to the manifest command:
//
//	docker build -t cobe-linter:dev .
//	go test -tags conformance ./...
//
// Set IMAGE=<tag> to test a different image. `sudo docker` is used
// automatically when the daemon is not reachable as the current user.
package tests

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const cliPath = "/usr/local/bin/cobe-lint"

func image() string {
	if img := os.Getenv("IMAGE"); img != "" {
		return img
	}
	return "cobe-linter:dev"
}

var (
	dockerOnce   sync.Once
	dockerPrefix []string
)

func dockerCmd() []string {
	dockerOnce.Do(func() {
		if exec.Command("docker", "info").Run() == nil {
			dockerPrefix = []string{"docker"}
			return
		}
		dockerPrefix = []string{"sudo", "docker"}
	})
	return dockerPrefix
}

// docker runs a docker CLI command and returns stdout, stderr, exit code.
func docker(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	argv := append(append([]string{}, dockerCmd()...), args...)
	cmd := exec.Command(argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	exitCode := 0
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("docker %v: %v", args, err)
		}
		exitCode = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), exitCode
}

// manifestEntry mirrors the contract §1 schema; workspace filenames are
// core's deployment config and no longer part of the manifest.
type manifestEntry struct {
	Command []string `json:"command"`
}

type manifestDoc struct {
	Version   int                      `json:"version"`
	Languages map[string]manifestEntry `json:"languages"`
}

type report struct {
	Version  int              `json:"version"`
	Language string           `json:"language"`
	Tool     string           `json:"tool"`
	Findings []map[string]any `json:"findings"`
}

func loadManifest(t *testing.T) manifestDoc {
	t.Helper()
	stdout, stderr, code := docker(t, "run", "--rm", image(), cliPath, "manifest")
	if code != 0 {
		t.Fatalf("cobe-lint manifest: exit %d, stderr: %s", code, stderr)
	}
	var m manifestDoc
	if err := json.Unmarshal([]byte(stdout), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v\n%s", err, stdout)
	}
	return m
}

// fixtureFile returns the single sample staged for lang/kind and its
// basename, which is used as the workspace filename (naming files is core's
// deployment config, so the fixtures carry their own lintable names).
func fixtureFile(t *testing.T, lang, kind string) (hostPath, name string) {
	t.Helper()
	dir := filepath.Join("testdata", lang, kind)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("fixture dir missing: %s (every manifest language needs clean+dirty samples under tests/testdata)", dir)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	if len(files) != 1 {
		t.Fatalf("%s: want exactly one fixture file, got %v", dir, files)
	}
	return filepath.Join(dir, files[0]), files[0]
}

// lintFixture mounts hostPath→name pairs into /workspace and invokes the
// manifest command with the mounted names appended, like core does.
func lintFixture(t *testing.T, entry manifestEntry, mounts map[string]string, files ...string) (string, string, int) {
	t.Helper()
	args := []string{"run", "--rm", "-w", "/workspace"}
	for host, name := range mounts {
		abs, err := filepath.Abs(host)
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, "-v", abs+":/workspace/"+name+":ro")
	}
	args = append(args, image())
	args = append(args, entry.Command...)
	args = append(args, files...)
	return docker(t, args...)
}

var severities = map[string]bool{
	"error": true, "warning": true, "convention": true, "refactor": true, "info": true,
}

// checkReport asserts the unified output schema (contract §2).
func checkReport(t *testing.T, stdout, lang string) report {
	t.Helper()
	var r report
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout)
	}
	if r.Version != 1 {
		t.Errorf("version = %d, want 1", r.Version)
	}
	if r.Language != lang {
		t.Errorf("language = %q, want %q", r.Language, lang)
	}
	if r.Tool == "" {
		t.Error("tool is empty")
	}
	if r.Findings == nil {
		t.Error("findings is missing or null; must be an array")
	}
	for i, f := range r.Findings {
		if path, _ := f["path"].(string); path == "" {
			t.Errorf("finding %d: missing path", i)
		}
		if line, ok := f["line"].(float64); !ok || line < 0 || line != float64(int(line)) {
			t.Errorf("finding %d: bad line %v", i, f["line"])
		}
		if sev, _ := f["severity"].(string); !severities[sev] {
			t.Errorf("finding %d: severity %q outside the contract enum", i, f["severity"])
		}
		if msg, _ := f["message"].(string); msg == "" {
			t.Errorf("finding %d: missing message", i)
		}
		if col, present := f["column"]; present {
			if c, ok := col.(float64); !ok || c < 0 || c != float64(int(c)) {
				t.Errorf("finding %d: bad column %v", i, col)
			}
		}
		if rule, present := f["rule"]; present {
			if s, ok := rule.(string); !ok || s == "" {
				t.Errorf("finding %d: bad rule %v", i, rule)
			}
		}
	}
	return r
}

func TestManifestSchema(t *testing.T) {
	m := loadManifest(t)
	if m.Version != 1 {
		t.Errorf("manifest version = %d, want 1", m.Version)
	}
	if len(m.Languages) == 0 {
		t.Fatal("manifest declares no languages")
	}
	for lang, entry := range m.Languages {
		if len(entry.Command) == 0 {
			t.Errorf("%s: empty command", lang)
			continue
		}
		for _, arg := range entry.Command {
			if strings.Contains(arg, "{") {
				t.Errorf("%s: command argument %q contains a placeholder; the command is a plain argv prefix", lang, arg)
			}
		}
		if !strings.HasPrefix(entry.Command[0], "/") {
			t.Errorf("%s: argv[0] %q is not an absolute path", lang, entry.Command[0])
		}
		if _, _, code := docker(t, "run", "--rm", image(), "test", "-x", entry.Command[0]); code != 0 {
			t.Errorf("%s: argv[0] %q is missing or not executable in the image", lang, entry.Command[0])
		}
	}
}

func TestLanguagesCleanAndDirty(t *testing.T) {
	m := loadManifest(t)
	for lang, entry := range m.Languages {
		for _, kind := range []string{"clean", "dirty"} {
			t.Run(lang+"/"+kind, func(t *testing.T) {
				fixture, name := fixtureFile(t, lang, kind)
				stdout, stderr, code := lintFixture(t, entry,
					map[string]string{fixture: name}, name)
				if code != 0 {
					t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr)
				}
				r := checkReport(t, stdout, lang)
				if kind == "dirty" && len(r.Findings) == 0 {
					t.Error("dirty sample produced no findings")
				}
				if kind == "clean" && len(r.Findings) != 0 {
					t.Errorf("clean sample produced findings: %v", r.Findings)
				}
			})
		}
	}
}

// Multi-file invocation: all files go to the tool in one run, and findings
// carry per-file paths — only the dirty file may produce any.
func TestMultiFilePython(t *testing.T) {
	m := loadManifest(t)
	entry, ok := m.Languages["python"]
	if !ok {
		t.Skip("manifest has no python")
	}
	cleanFixture, _ := fixtureFile(t, "python", "clean")
	dirtyFixture, _ := fixtureFile(t, "python", "dirty")
	mounts := map[string]string{
		cleanFixture: "clean.py",
		dirtyFixture: "dirty.py",
	}
	stdout, stderr, code := lintFixture(t, entry, mounts, "clean.py", "dirty.py")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr)
	}
	r := checkReport(t, stdout, "python")
	if len(r.Findings) == 0 {
		t.Fatal("multi-file lint produced no findings")
	}
	for i, f := range r.Findings {
		if f["path"] != "dirty.py" {
			t.Errorf("finding %d: path %v, want every finding under dirty.py", i, f["path"])
		}
	}
}

func TestUnknownLanguageFails(t *testing.T) {
	_, stderr, code := docker(t, "run", "--rm", image(), cliPath, "lint", "klingon", "solution.py")
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero; stderr: %s", stderr)
	}
	if !strings.Contains(stderr, "unknown language") {
		t.Errorf("stderr = %q, want an unknown-language diagnostic", stderr)
	}
}

func TestMissingFileFails(t *testing.T) {
	m := loadManifest(t)
	entry, ok := m.Languages["python"]
	if !ok {
		t.Skip("manifest has no python")
	}
	args := append([]string{"run", "--rm", "-w", "/workspace", image()}, entry.Command...)
	args = append(args, "no-such-file.py")
	_, stderr, code := docker(t, args...)
	if code == 0 {
		t.Fatal("exit = 0, want non-zero")
	}
	if !strings.Contains(stderr, "not readable") {
		t.Errorf("stderr = %q, want a not-readable diagnostic", stderr)
	}
}
