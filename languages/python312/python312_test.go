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
	want := []string{"/opt/python/" + PythonVersion + "/bin/pylint", "--output-format=json", "--disable=C0114,C0115,C0116", "a.py", "b.py"}
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
		Path: "solution.py", Line: 5, Column: 5, // pylint column 4, 0-based
		Severity: linter.SeverityWarning, Rule: "W0612", Message: "Unused variable 'unused'",
	}
	if got != want {
		t.Errorf("finding[0] = %+v, want %+v", got, want)
	}
	if !strings.HasPrefix(report.Tool, "pylint") {
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
