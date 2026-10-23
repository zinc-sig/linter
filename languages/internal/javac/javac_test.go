package javac

import (
	"slices"
	"strings"
	"testing"

	"github.com/zinc-sig/linter/linter"
)

// newTest builds the driver exactly as the java manifest stanza does.
func newTest() *Linter { return New("java", "Java", "11") }

func TestMetadata(t *testing.T) {
	l := newTest()
	if l.Language() != "java" || l.Name() != "Java" || l.Release() != "11" {
		t.Errorf("metadata = %q %q %q", l.Language(), l.Name(), l.Release())
	}
}

func TestCommand(t *testing.T) {
	got := newTest().Command([]string{"A.java", "B.java"})
	want := []string{"/opt/java/bin/java", "-XX:+UseSerialGC", "-XX:-UsePerfData", "-jar", "/opt/javac-worker.jar",
		"--once", "--release", "11", "A.java", "B.java"}
	if !slices.Equal(got, want) {
		t.Errorf("Command = %v, want %v", got, want)
	}
}

func TestParseDirty(t *testing.T) {
	report, err := newTest().Parse([]byte(dirtyStdout), nil, 0)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []linter.Finding{
		{
			Path: "Solution.java", Line: 5, Column: 44, EndLine: 5, EndColumn: 50,
			Severity: linter.SeverityError, Rule: "compiler.err.cant.resolve.location.args",
			Message: "cannot find symbol\n  symbol:   method toList()\n  location: interface java.util.stream.Stream<java.lang.Integer>",
		},
		{
			Path: "Solution.java", Line: 6, Column: 11, EndLine: 6, EndColumn: 13,
			Severity: linter.SeverityError, Rule: "compiler.err.prob.found.req",
			Message: "incompatible types: java.lang.String cannot be converted to int",
		},
	}
	if !slices.Equal(report.Findings, want) {
		t.Errorf("findings = %+v, want %+v", report.Findings, want)
	}
	if report.Tool != "javac 21.0.12.1 --release 11" || report.ToolID != "javac" {
		t.Errorf("tool = %q, tool_id = %q", report.Tool, report.ToolID)
	}
	if report.Version != 1 || report.Language != "java" {
		t.Errorf("header = %d/%q", report.Version, report.Language)
	}
}

func TestParseClean(t *testing.T) {
	report, err := newTest().Parse([]byte(cleanStdout), nil, 0)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if report.Findings == nil || len(report.Findings) != 0 {
		t.Errorf("findings = %#v, want empty non-nil slice", report.Findings)
	}
}

// A source javac cannot parse is a finding, not an operational failure.
func TestParseSyntaxErrorIsAFinding(t *testing.T) {
	report, err := newTest().Parse([]byte(brokenStdout), nil, 0)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Rule != "compiler.err.premature.eof" || report.Findings[0].EndLine != 0 {
		t.Errorf("findings = %+v", report.Findings)
	}
}

func TestParseCrashIsOperationalFailure(t *testing.T) {
	_, err := newTest().Parse(nil, []byte(crashStderr), crashExitCode)
	if err == nil || !strings.Contains(err.Error(), "release 99 is not supported") {
		t.Fatalf("err = %v, want the worker's stderr", err)
	}
}

func TestParseRejectsMalformedReports(t *testing.T) {
	cases := map[string]string{
		"not JSON":         "<?xml version=\"1.0\"?>",
		"no findings":      `{"tool":"javac"}`,
		"unknown severity": `{"tool":"javac","findings":[{"path":"A.java","line":1,"severity":"fatal","message":"m"}]}`,
	}
	for name, stdout := range cases {
		if _, err := newTest().Parse([]byte(stdout), nil, 0); err == nil {
			t.Errorf("%s: Parse accepted %s", name, stdout)
		}
	}
}
