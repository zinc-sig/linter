package c

import (
	"slices"
	"strings"
	"testing"

	"github.com/zinc-sig/linter/linter"
)

func TestMetadata(t *testing.T) {
	l := New()
	if l.Language() != "c" {
		t.Errorf("Language = %q", l.Language())
	}
}

func TestCommand(t *testing.T) {
	got := New().Command([]string{"a.c", "b.c"})
	want := []string{"clang-tidy", "a.c", "b.c", "--", "-std=" + CStandard}
	if !slices.Equal(got, want) {
		t.Errorf("Command = %v, want %v", got, want)
	}
}

// The dirty fixture contains one warning followed by two "note:" lines and
// indented source-context lines — only the warning is a finding.
func TestParseDirtySkipsNotes(t *testing.T) {
	report, err := New().Parse([]byte(dirtyStdout), []byte(dirtyStderr), dirtyExitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want 1 (notes must be skipped): %+v", len(report.Findings), report.Findings)
	}
	got := report.Findings[0]
	want := linter.Finding{
		Path: "/workspace/solution.c", Line: 5, Column: 8,
		Severity: linter.SeverityWarning, Rule: "clang-analyzer-core.NullDereference",
		Message: "Dereference of null pointer (loaded from variable 'p')",
	}
	if got != want {
		t.Errorf("finding = %+v, want %+v", got, want)
	}
	if !strings.HasPrefix(report.Tool, "clang-tidy") {
		t.Errorf("tool = %q", report.Tool)
	}
	if report.Version != 1 || report.Language != "c" {
		t.Errorf("header = %d/%q", report.Version, report.Language)
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

// clang-tidy exits 1 on compile errors, but the diagnostics parse fine —
// findings, not an operational failure.
func TestParseCompileErrorIsFindings(t *testing.T) {
	report, err := New().Parse([]byte(compileErrorStdout), []byte(compileErrorStderr), compileErrorExitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(report.Findings) != 2 {
		t.Fatalf("findings = %+v, want 2 compile errors", report.Findings)
	}
	for i, f := range report.Findings {
		if f.Severity != linter.SeverityError || f.Rule != "clang-diagnostic-error" {
			t.Errorf("finding %d = %+v, want error/clang-diagnostic-error", i, f)
		}
	}
}

// A non-zero exit with no parseable diagnostics is an operational failure.
func TestParseOperationalFailure(t *testing.T) {
	if _, err := New().Parse(nil, []byte("Segmentation fault"), 139); err == nil {
		t.Fatal("Parse must fail on a non-zero exit without diagnostics")
	}
}

// remark severities map to info; a diagnostic without a [rule] suffix has
// no rule.
func TestParseRemarkAndRuleless(t *testing.T) {
	stdout := []byte("/w/a.c:1:2: remark: something\n/w/a.c:3:4: warning: bare warning\n")
	report, err := New().Parse(stdout, nil, 0)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []linter.Finding{
		{Path: "/w/a.c", Line: 1, Column: 2, Severity: linter.SeverityInfo, Message: "something"},
		{Path: "/w/a.c", Line: 3, Column: 4, Severity: linter.SeverityWarning, Message: "bare warning"},
	}
	if !slices.Equal(report.Findings, want) {
		t.Errorf("findings = %+v, want %+v", report.Findings, want)
	}
}
