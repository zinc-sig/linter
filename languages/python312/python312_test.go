package python312

import (
	"slices"
	"strings"
	"testing"

	"github.com/zinc-sig/linter/linter"
)

func TestMetadata(t *testing.T) {
	l := New()
	if l.Language() != "python312" {
		t.Errorf("Language = %q", l.Language())
	}
	if l.Name() != "Python 3.12" {
		t.Errorf("Name = %q", l.Name())
	}
}

func TestCommand(t *testing.T) {
	got := New().Command([]string{"a.py", "b.py"})
	want := []string{"/usr/local/bin/ruff", "check", "--no-cache", "--output-format=json", "--target-version", "py312", "a.py", "b.py"}
	if !slices.Equal(got, want) {
		t.Errorf("Command = %v, want %v", got, want)
	}
}

func TestParseDirty(t *testing.T) {
	report, err := New().Parse([]byte(dirtyStdout), nil, dirtyExitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(report.Findings) != 2 {
		t.Fatalf("findings = %d, want 2: %+v", len(report.Findings), report.Findings)
	}
	got := report.Findings[0]
	want := linter.Finding{
		// ruff reports absolute paths; linter.Run normalizes them back to
		// the invocation paths after Parse.
		Path: "/workspace/solution.py", Line: 1, Column: 8,
		Severity: linter.SeverityWarning, Rule: "F401", Message: "`os` imported but unused",
	}
	if got != want {
		t.Errorf("finding[0] = %+v, want %+v", got, want)
	}
	if f := report.Findings[1]; f.Rule != "F821" || f.Severity != linter.SeverityError {
		t.Errorf("finding[1] = %+v, want F821/error (undefined names surface to students)", f)
	}
	if !strings.HasPrefix(report.Tool, "ruff") {
		t.Errorf("tool = %q", report.Tool)
	}
	if report.Version != 1 || report.Language != "python312" {
		t.Errorf("header = %d/%q", report.Version, report.Language)
	}
}

func TestParseClean(t *testing.T) {
	report, err := New().Parse([]byte(cleanStdout), nil, cleanExitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if report.Findings == nil || len(report.Findings) != 0 {
		t.Errorf("findings = %#v, want empty non-nil slice", report.Findings)
	}
}
