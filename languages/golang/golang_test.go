package golang

import (
	"slices"
	"strings"
	"testing"

	"github.com/zinc-sig/linter/linter"
)

func TestMetadata(t *testing.T) {
	l := New()
	if l.Language() != "go" {
		t.Errorf("Language = %q", l.Language())
	}
}

func TestCommand(t *testing.T) {
	got := New().Command([]string{"a.go", "b.go"})
	want := []string{"go", "vet", "a.go", "b.go"}
	if !slices.Equal(got, want) {
		t.Errorf("Command = %v, want %v", got, want)
	}
}

func TestEnvDefaults(t *testing.T) {
	env := New().(linter.Enver).Env()
	for _, key := range []string{"GOCACHE=", "GOPATH=", "GOTOOLCHAIN=local", "GOPROXY=off", "GOMAXPROCS=2"} {
		found := false
		for _, kv := range env {
			if strings.HasPrefix(kv, key) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Env() missing %q: %v", key, env)
		}
	}
}

// The dirty fixture: "#" package headers must be skipped, the printf
// diagnostic parsed as a warning with the tool's "./" path kept verbatim
// (normalization happens in linter.Run).
func TestParseDirty(t *testing.T) {
	report, err := New().Parse(nil, []byte(dirtyStderr), dirtyExitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(report.Findings), report.Findings)
	}
	got := report.Findings[0]
	want := linter.Finding{
		Path: "./solution.go", Line: 7, Column: 2,
		Severity: linter.SeverityWarning,
		Message:  "fmt.Printf format %d has arg name of wrong type string",
	}
	if got != want {
		t.Errorf("finding = %+v, want %+v", got, want)
	}
	if !strings.HasPrefix(report.Tool, "go vet") {
		t.Errorf("tool = %q", report.Tool)
	}
	if report.Version != 1 || report.Language != "go" {
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

// Compile/typecheck failures come prefixed with "vet: " and map to
// severity error — still data, exit 0 for the CLI.
func TestParseCompileErrorIsErrorFinding(t *testing.T) {
	report, err := New().Parse(nil, []byte(compileErrorStderr), compileErrorExitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %+v, want 1", report.Findings)
	}
	got := report.Findings[0]
	want := linter.Finding{
		Path: "./solution.go", Line: 4, Column: 7,
		Severity: linter.SeverityError, Message: "undefined: foo",
	}
	if got != want {
		t.Errorf("finding = %+v, want %+v", got, want)
	}
}

// A non-zero exit with no parseable diagnostics (e.g. bad invocation or
// mixed-package file sets) is an operational failure.
func TestParseOperationalFailure(t *testing.T) {
	stderr := []byte("named files must all be in one directory; have a and b\n")
	if _, err := New().Parse(nil, stderr, 1); err == nil {
		t.Fatal("Parse must fail on a non-zero exit without diagnostics")
	}
}

func TestParseColumnOptional(t *testing.T) {
	report, err := New().Parse(nil, []byte("./solution.go:3: file-scoped complaint\n"), 1)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []linter.Finding{{
		Path: "./solution.go", Line: 3,
		Severity: linter.SeverityWarning, Message: "file-scoped complaint",
	}}
	if !slices.Equal(report.Findings, want) {
		t.Errorf("findings = %+v, want %+v", report.Findings, want)
	}
}
