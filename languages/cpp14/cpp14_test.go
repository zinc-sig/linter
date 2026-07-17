package cpp14

import (
	"slices"
	"testing"

	"github.com/zinc-sig/linter/linter"
)

func TestMetadata(t *testing.T) {
	l := New()
	if l.Language() != "cpp14" {
		t.Errorf("Language = %q", l.Language())
	}
}

func TestCommand(t *testing.T) {
	got := New().Command([]string{"a.cpp"})
	want := []string{"clang-tidy", "a.cpp", "--", "-std=" + CppStandard}
	if !slices.Equal(got, want) {
		t.Errorf("Command = %v, want %v", got, want)
	}
}

func TestParseDirty(t *testing.T) {
	report, err := New().Parse([]byte(dirtyStdout), []byte(dirtyStderr), dirtyExitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(report.Findings), report.Findings)
	}
	got := report.Findings[0]
	want := linter.Finding{
		Path: "/workspace/solution.cpp", Line: 5, Column: 8,
		Severity: linter.SeverityWarning, Rule: "clang-analyzer-core.NullDereference",
		Message: "Dereference of null pointer (loaded from variable 'p')",
	}
	if got != want {
		t.Errorf("finding = %+v, want %+v", got, want)
	}
	if report.Language != "cpp14" {
		t.Errorf("language = %q", report.Language)
	}
	if report.ToolID != "clang-tidy" {
		t.Errorf("tool_id = %q, want clang-tidy", report.ToolID)
	}
}

func TestParseClean(t *testing.T) {
	report, err := New().Parse(nil, nil, cleanExitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if report.Findings == nil || len(report.Findings) != 0 {
		t.Errorf("findings = %#v, want empty non-nil slice", report.Findings)
	}
}
