package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/zinc-sig/linter/languages"
	"github.com/zinc-sig/linter/linter"
)

func TestManifest(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"manifest"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, stderr.String())
	}
	var m manifest
	if err := json.Unmarshal(stdout.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v\n%s", err, stdout.String())
	}
	if m.Version != 1 {
		t.Errorf("version = %d, want 1", m.Version)
	}
	if len(m.Languages) != len(languages.All()) {
		t.Errorf("languages = %d, want %d", len(m.Languages), len(languages.All()))
	}
	entry, ok := m.Languages["python"]
	if !ok {
		t.Fatal("manifest is missing python")
	}
	if entry.Filename != "solution.py" {
		t.Errorf("python filename = %q", entry.Filename)
	}
	// The command is a plain argv prefix — no {file}/{files} placeholders;
	// consumers append file paths as trailing arguments.
	want := []string{binPath, "lint", "python"}
	if !slices.Equal(entry.Command, want) {
		t.Errorf("python command = %v, want %v", entry.Command, want)
	}
	for lang, e := range m.Languages {
		for _, arg := range e.Command {
			if strings.Contains(arg, "{") {
				t.Errorf("%s: command argument %q contains a placeholder", lang, arg)
			}
		}
	}
}

func TestUsageErrors(t *testing.T) {
	cases := [][]string{
		{},
		{"bogus-command"},
		{"lint"},
		{"lint", "python"}, // no files
		{"manifest", "extra"},
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("run(%v) = %d, want 2", args, code)
		}
		if stderr.Len() == 0 {
			t.Errorf("run(%v): no diagnostic on stderr", args)
		}
	}
}

func TestUnknownLanguage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "klingon", "x.py"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown language") ||
		!strings.Contains(stderr.String(), "python") {
		t.Errorf("stderr = %q, want unknown-language diagnostic listing supported keys", stderr.String())
	}
}

func TestMissingFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "python", "/no/such/file.py"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "not readable") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// End-to-end through linter.Run with the host Go toolchain: a real
// `go vet` on a dirty bare file, exercising env defaults and path
// normalization.
func TestLintGoEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not in PATH")
	}
	dir := t.TempDir()
	src := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tname := \"world\"\n\tfmt.Printf(\"%d\\n\", name)\n}\n"
	if err := os.WriteFile(dir+"/solution.go", []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "go", "solution.go"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr.String())
	}
	var report linter.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if report.Version != 1 || report.Language != "go" {
		t.Errorf("header = %d/%q", report.Version, report.Language)
	}
	if len(report.Findings) == 0 {
		t.Fatal("dirty file produced no findings")
	}
	f := report.Findings[0]
	if f.Path != "solution.go" {
		t.Errorf("path = %q, want normalized solution.go", f.Path)
	}
	if f.Severity != linter.SeverityWarning || !strings.Contains(f.Message, "fmt.Printf") {
		t.Errorf("finding = %+v", f)
	}
}
