package sqlsyntax

import (
	"slices"
	"strings"
	"testing"

	"github.com/zinc-sig/linter/linter"
)

// newTest builds the driver exactly as the mysql manifest stanza does;
// the driver is dialect-agnostic, so one dialect exercises every path.
func newTest() *Linter { return New("mysql", "MySQL", "mysql") }

// Inline fixtures: real sqlsyntax output captured by running
// /usr/local/bin/sqlsyntax --dialect mysql answer.sql in the image; the
// *ExitCode consts are the recorded exit statuses of those runs.
const cleanExitCode = 0

const cleanStdout = `{"diagnostics":[]}
`

const dirtyExitCode = 1

const dirtyStdout = `{"diagnostics":[{"file":"answer.sql","line":1,"column":16,"end_line":1,"end_column":22,"message":"syntax error near \"student\""},{"file":"answer.sql","line":3,"column":28,"end_line":3,"end_column":28,"message":"syntax error at the end of the statement"}]}
`

func TestMetadata(t *testing.T) {
	l := newTest()
	if l.Name() != "MySQL" {
		t.Errorf("Name = %q", l.Name())
	}
	if l.Language() != "mysql" {
		t.Errorf("Language = %q", l.Language())
	}
}

func TestCommand(t *testing.T) {
	got := New("postgresql", "PostgreSQL", "postgresql").Command([]string{"a.sql", "b.sql"})
	want := []string{"/usr/local/bin/sqlsyntax", "--dialect", "postgresql", "a.sql", "b.sql"}
	if !slices.Equal(got, want) {
		t.Errorf("Command = %v, want %v", got, want)
	}
}

func TestParseDirty(t *testing.T) {
	report, err := newTest().Parse([]byte(dirtyStdout), nil, dirtyExitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []linter.Finding{
		{
			Path: "answer.sql", Line: 1, Column: 16, EndLine: 1, EndColumn: 22,
			Severity: linter.SeverityError, Rule: "syntax-error", Message: `syntax error near "student"`,
		},
		{
			Path: "answer.sql", Line: 3, Column: 28, EndLine: 3, EndColumn: 28,
			Severity: linter.SeverityError, Rule: "syntax-error", Message: "syntax error at the end of the statement",
		},
	}
	if !slices.Equal(report.Findings, want) {
		t.Errorf("findings = %+v, want %+v", report.Findings, want)
	}
	if !strings.HasPrefix(report.Tool, "sqlsyntax") {
		t.Errorf("tool = %q", report.Tool)
	}
	if report.ToolID != "sqlsyntax" {
		t.Errorf("tool_id = %q, want sqlsyntax", report.ToolID)
	}
	if report.Language != "mysql" || report.Version != linter.ReportVersion {
		t.Errorf("language/version = %q/%d", report.Language, report.Version)
	}
}

func TestParseClean(t *testing.T) {
	report, err := newTest().Parse([]byte(cleanStdout), nil, cleanExitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if report.Findings == nil || len(report.Findings) != 0 {
		t.Errorf("findings = %#v, want empty non-nil slice", report.Findings)
	}
}

// Exit 2 is the command's "could not run"; a document it may still have
// printed does not turn that into data.
func TestParseUsageErrorIsOperationalFailure(t *testing.T) {
	_, err := newTest().Parse([]byte(cleanStdout), []byte("sqlsyntax: open answer.sql: no such file or directory"), 2)
	if err == nil {
		t.Fatal("Parse must fail on exit 2")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error %q does not carry the stderr text", err)
	}
}

func TestParseGarbageIsOperationalFailure(t *testing.T) {
	if _, err := newTest().Parse([]byte("not json"), nil, 1); err == nil {
		t.Fatal("Parse must fail on unparseable output")
	}
}
