package linter

import (
	"strings"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	cases := []struct {
		reported string
		files    []string
		want     string
	}{
		{"solution.py", []string{"solution.py"}, "solution.py"},
		{"./solution.go", []string{"solution.go"}, "solution.go"},
		{"/workspace/solution.c", []string{"solution.c"}, "solution.c"},
		{"/workspace/Solution.java", []string{"Solution.java"}, "Solution.java"},
		{"/workspace/pkg/a.go", []string{"pkg/a.go", "a.go"}, "pkg/a.go"},
		{"/workspace/x.go", []string{"a/x.go", "x.go"}, "x.go"},
		{"/usr/include/stdio.h", []string{"solution.c"}, "/usr/include/stdio.h"},
		{"./dir/../f.py", []string{"f.py"}, "f.py"},
	}
	for _, c := range cases {
		if got := NormalizePath(c.reported, c.files); got != c.want {
			t.Errorf("NormalizePath(%q, %v) = %q, want %q", c.reported, c.files, got, c.want)
		}
	}
}

// fake is a scriptable Linter for exercising Run.
type fake struct {
	argv  []string
	parse func(stdout, stderr []byte, exitCode int) (Report, error)
}

func (f fake) Language() string                { return "fake" }
func (f fake) Name() string                    { return "Fake" }
func (f fake) Command(files []string) []string { return f.argv }
func (f fake) Parse(stdout, stderr []byte, exitCode int) (Report, error) {
	return f.parse(stdout, stderr, exitCode)
}

type fakeWithEnv struct {
	fake
	env []string
}

func (f fakeWithEnv) Env() []string { return f.env }

func TestRunCapturesOutputAndExitCode(t *testing.T) {
	l := fake{
		argv: []string{"sh", "-c", "echo from-stdout; echo from-stderr >&2; exit 3"},
		parse: func(stdout, stderr []byte, exitCode int) (Report, error) {
			if got := strings.TrimSpace(string(stdout)); got != "from-stdout" {
				t.Errorf("stdout = %q", got)
			}
			if got := strings.TrimSpace(string(stderr)); got != "from-stderr" {
				t.Errorf("stderr = %q", got)
			}
			if exitCode != 3 {
				t.Errorf("exitCode = %d, want 3", exitCode)
			}
			return Report{
				Version:  ReportVersion,
				Language: "fake",
				Tool:     "fake 1.0",
				Findings: []Finding{{Path: "/workspace/solution.c", Line: 1, Severity: SeverityWarning, Message: "m"}},
			}, nil
		},
	}
	report, err := Run(l, []string{"solution.c"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := report.Findings[0].Path; got != "solution.c" {
		t.Errorf("finding path = %q, want normalized %q", got, "solution.c")
	}
}

func TestRunMissingBinary(t *testing.T) {
	l := fake{
		argv: []string{"/no/such/binary-cobe-test"},
		parse: func([]byte, []byte, int) (Report, error) {
			t.Fatal("Parse must not be called when the tool cannot start")
			return Report{}, nil
		},
	}
	if _, err := Run(l, nil); err == nil {
		t.Fatal("Run with a missing binary must return an operational failure")
	}
}

func TestRunKilledBySignal(t *testing.T) {
	l := fake{
		argv: []string{"sh", "-c", "kill -KILL $$"},
		parse: func([]byte, []byte, int) (Report, error) {
			t.Fatal("Parse must not be called for a signal-killed tool")
			return Report{}, nil
		},
	}
	if _, err := Run(l, nil); err == nil {
		t.Fatal("Run on a signal-killed tool must return an operational failure")
	}
}

func TestRunEnvDefaults(t *testing.T) {
	l := fakeWithEnv{
		fake: fake{argv: []string{"sh", "-c", `printf '%s' "$COBE_LINT_TEST_VAR"`}},
		env:  []string{"COBE_LINT_TEST_VAR=from-default"},
	}
	l.parse = func(stdout, _ []byte, _ int) (Report, error) {
		return Report{Tool: string(stdout), Findings: []Finding{}}, nil
	}
	report, err := Run(l, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Tool != "from-default" {
		t.Errorf("default env not applied: got %q", report.Tool)
	}

	t.Setenv("COBE_LINT_TEST_VAR", "explicit")
	report, err = Run(l, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Tool != "explicit" {
		t.Errorf("explicit env must win over defaults: got %q", report.Tool)
	}
}

func TestToolVersion(t *testing.T) {
	if got := ToolVersion("echo tool", `version (\S+)`, "echo", "version 9.9"); got != "echo tool 9.9" {
		t.Errorf("ToolVersion = %q", got)
	}
	if got := ToolVersion("mytool", `version (\S+)`, "/no/such/binary-cobe-test"); got != "mytool" {
		t.Errorf("ToolVersion fallback = %q, want bare name", got)
	}
}
