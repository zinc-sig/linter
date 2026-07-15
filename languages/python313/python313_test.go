package python313

import (
	"slices"
	"strings"
	"testing"

	"github.com/zinc-sig/linter/linter"
)

func TestMetadata(t *testing.T) {
	l := New()
	if l.Name() != "Python 3.13" {
		t.Errorf("Name = %q", l.Name())
	}
	if l.Language() != "python313" {
		t.Errorf("Language = %q", l.Language())
	}
}

func TestCommand(t *testing.T) {
	got := New().Command([]string{"a.py", "b.py"})
	want := []string{"/usr/local/bin/ruff", "check", "--no-cache", "--output-format=json", "--target-version", "py313", "a.py", "b.py"}
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
	if f := report.Findings[1]; f.Rule != "F821" || f.Severity != linter.SeverityError || f.Line != 5 || f.Column != 11 {
		t.Errorf("finding[1] = %+v, want F821/error at 5:11 (undefined names surface to students)", f)
	}
	if !strings.HasPrefix(report.Tool, "ruff") {
		t.Errorf("tool = %q", report.Tool)
	}
	if report.Version != 1 || report.Language != "python313" {
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

// A Python syntax error makes ruff exit 1, but the invalid-syntax
// diagnostics are still valid JSON — findings, not a failure.
func TestParseSyntaxErrorIsFinding(t *testing.T) {
	report, err := New().Parse([]byte(syntaxErrorStdout), nil, syntaxErrorExitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(report.Findings) != 2 {
		t.Fatalf("findings = %+v", report.Findings)
	}
	for i, f := range report.Findings {
		if f.Severity != linter.SeverityError || f.Rule != "invalid-syntax" {
			t.Errorf("finding %d = %+v, want invalid-syntax/error", i, f)
		}
	}
}

// Exit 2 is a ruff usage/internal error: an operational failure.
func TestParseUsageError(t *testing.T) {
	if _, err := New().Parse(nil, []byte(usageErrorStderr), usageErrorExitCode); err == nil {
		t.Fatal("Parse must fail on exit 2")
	} else if !strings.Contains(err.Error(), "exit 2") {
		t.Errorf("err = %v", err)
	}
}

func TestParseMultiFile(t *testing.T) {
	report, err := New().Parse([]byte(multifileStdout), nil, multifileExitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(report.Findings) == 0 {
		t.Fatal("no findings")
	}
	for _, f := range report.Findings {
		if f.Path != "/workspace/dirty.py" {
			t.Errorf("finding path = %q, want /workspace/dirty.py only", f.Path)
		}
	}
}

func TestParseGarbage(t *testing.T) {
	if _, err := New().Parse([]byte("not json"), nil, 0); err == nil {
		t.Fatal("Parse must fail on unparseable output")
	}
}

func TestSeverityMapping(t *testing.T) {
	stdout := []byte(`[
		{"code": null, "message": "syntax error (older ruff releases)"},
		{"code": "invalid-syntax", "message": "syntax error"},
		{"code": "E999", "message": "syntax error (historic code)"},
		{"code": "F821", "message": "undefined name"},
		{"code": "F822", "message": "undefined export"},
		{"code": "F823", "message": "undefined local"},
		{"code": "F401", "message": "unused import"},
		{"code": "E701", "message": "multiple statements on one line"},
		{"code": "W605", "message": "invalid escape sequence"},
		{"code": "C901", "message": "too complex (not in the default rules)"}
	]`)
	report, err := New().Parse(stdout, nil, 1)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{
		linter.SeverityError, linter.SeverityError, linter.SeverityError,
		linter.SeverityError, linter.SeverityError, linter.SeverityError,
		linter.SeverityWarning,    // other F*: pyflakes code smells
		linter.SeverityConvention, // other E*: pycodestyle style errors
		linter.SeverityWarning,    // W*: pycodestyle warnings
		linter.SeverityWarning,    // unknown codes default to warning
	}
	if len(report.Findings) != len(want) {
		t.Fatalf("findings = %d, want %d", len(report.Findings), len(want))
	}
	for i, f := range report.Findings {
		if f.Severity != want[i] {
			t.Errorf("finding %d (%s) severity = %q, want %q", i, f.Rule, f.Severity, want[i])
		}
	}
}
