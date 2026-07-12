package python

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/zinc-sig/linter/linter"
)

// loadCase reads a captured native-tool run from testdata: real pylint
// output recorded from the image (see <case>.exit for the exit status).
func loadCase(t *testing.T, name string) (stdout, stderr []byte, exitCode int) {
	t.Helper()
	stdout, err := os.ReadFile(filepath.Join("testdata", name+".stdout"))
	if err != nil {
		t.Fatal(err)
	}
	stderr, err = os.ReadFile(filepath.Join("testdata", name+".stderr"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("testdata", name+".exit"))
	if err != nil {
		t.Fatal(err)
	}
	exitCode, err = strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return stdout, stderr, exitCode
}

func TestMetadata(t *testing.T) {
	l := New()
	if l.Language() != "python" {
		t.Errorf("Language = %q", l.Language())
	}
	if l.Filename() != "solution.py" {
		t.Errorf("Filename = %q", l.Filename())
	}
}

func TestCommand(t *testing.T) {
	got := New().Command([]string{"a.py", "b.py"})
	want := []string{"pylint", "--output-format=json", "--disable=C0114,C0115,C0116", "a.py", "b.py"}
	if !slices.Equal(got, want) {
		t.Errorf("Command = %v, want %v", got, want)
	}
}

func TestParseDirty(t *testing.T) {
	report, err := New().Parse(loadCase(t, "dirty"))
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
	if f := report.Findings[1]; f.Rule != "W0611" || f.Line != 1 || f.Column != 1 {
		t.Errorf("finding[1] = %+v, want W0611 at 1:1 (pylint column 0 maps to 1)", f)
	}
	if !strings.HasPrefix(report.Tool, "pylint") {
		t.Errorf("tool = %q", report.Tool)
	}
	if report.Version != 1 || report.Language != "python" {
		t.Errorf("header = %d/%q", report.Version, report.Language)
	}
}

func TestParseClean(t *testing.T) {
	report, err := New().Parse(loadCase(t, "clean"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if report.Findings == nil || len(report.Findings) != 0 {
		t.Errorf("findings = %#v, want empty non-nil slice", report.Findings)
	}
}

// A Python syntax error makes pylint exit with the error bit set, but the
// E0001 message is still valid JSON — a finding, not a failure.
func TestParseSyntaxErrorIsFinding(t *testing.T) {
	stdout, stderr, exitCode := loadCase(t, "syntax_error")
	if exitCode&2 == 0 {
		t.Fatalf("fixture exit = %d, expected the error bit", exitCode)
	}
	report, err := New().Parse(stdout, stderr, exitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %+v", report.Findings)
	}
	if f := report.Findings[0]; f.Severity != linter.SeverityError || f.Rule != "E0001" {
		t.Errorf("finding = %+v, want E0001/error", f)
	}
}

// Exit bit 32 is a pylint usage error: an operational failure.
func TestParseUsageError(t *testing.T) {
	stdout, stderr, exitCode := loadCase(t, "usage_error")
	if exitCode != 32 {
		t.Fatalf("fixture exit = %d, want 32", exitCode)
	}
	if _, err := New().Parse(stdout, stderr, exitCode); err == nil {
		t.Fatal("Parse must fail on the usage-error bit")
	} else if !strings.Contains(err.Error(), "usage error") {
		t.Errorf("err = %v", err)
	}
}

func TestParseMultiFile(t *testing.T) {
	report, err := New().Parse(loadCase(t, "multifile"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(report.Findings) == 0 {
		t.Fatal("no findings")
	}
	for _, f := range report.Findings {
		if f.Path != "dirty.py" {
			t.Errorf("finding path = %q, want dirty.py only", f.Path)
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
		{"message-id": "E0602", "type": "error"},
		{"message-id": "F0010", "type": "fatal"},
		{"message-id": "W0611", "type": "warning"},
		{"message-id": "C0301", "type": "convention"},
		{"message-id": "R0914", "type": "refactor"},
		{"message-id": "I0011", "type": "info"},
		{"message-id": "", "type": "refactor"},
		{"message-id": "X9999", "type": ""}
	]`)
	report, err := New().Parse(stdout, nil, 0)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{
		linter.SeverityError, linter.SeverityError, linter.SeverityWarning,
		linter.SeverityConvention, linter.SeverityRefactor, linter.SeverityInfo,
		linter.SeverityRefactor, // falls back to the "type" field
		linter.SeverityWarning,  // unknown everything defaults to warning
	}
	for i, f := range report.Findings {
		if f.Severity != want[i] {
			t.Errorf("finding %d severity = %q, want %q", i, f.Severity, want[i])
		}
	}
}
