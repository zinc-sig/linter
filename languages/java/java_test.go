package java

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/zinc-sig/linter/linter"
)

// loadCase reads a captured native-tool run from testdata: real checkstyle
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
	if l.Language() != "java" {
		t.Errorf("Language = %q", l.Language())
	}
	if l.Filename() != "Solution.java" {
		t.Errorf("Filename = %q", l.Filename())
	}
}

func TestCommand(t *testing.T) {
	got := New().Command([]string{"A.java", "B.java"})
	want := []string{"java", "-jar", "/opt/checkstyle.jar", "-c", "/opt/checkstyle-config.xml", "-f", "xml", "A.java", "B.java"}
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
		Path: "/workspace/Solution.java", Line: 1, Column: 17,
		Severity: linter.SeverityError, Rule: "AvoidStarImport",
		Message: "Using the '.*' form of import should be avoided - java.util.*.",
	}
	if got != want {
		t.Errorf("finding[0] = %+v, want %+v", got, want)
	}
	if f := report.Findings[1]; f.Rule != "NeedBraces" || f.Line != 5 {
		t.Errorf("finding[1] = %+v, want NeedBraces at line 5", f)
	}
	if report.Tool != "checkstyle 10.21.1" {
		t.Errorf("tool = %q, want version from the XML root attribute", report.Tool)
	}
	if report.Version != 1 || report.Language != "java" {
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

// Checkstyle throws (exit 254, no XML) on Java it cannot parse — an
// operational failure, unlike pylint which reports syntax errors as data.
func TestParseCrashIsOperationalFailure(t *testing.T) {
	stdout, stderr, exitCode := loadCase(t, "crash")
	if exitCode != 254 {
		t.Fatalf("fixture exit = %d, want 254", exitCode)
	}
	_, err := New().Parse(stdout, stderr, exitCode)
	if err == nil {
		t.Fatal("Parse must fail when checkstyle emits no XML")
	}
	if !strings.Contains(err.Error(), "no XML report") {
		t.Errorf("err = %v", err)
	}
}

func TestSeverityMappingAndOptionalAttrs(t *testing.T) {
	stdout := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<checkstyle version="10.21.1">
<file name="/workspace/Solution.java">
<error line="3" severity="warning" message="w" source="x.y.FooCheck"/>
<error line="4" severity="info" message="i" source="x.y.Bar"/>
<error line="5" severity="ignore" message="g"/>
<error line="6" severity="bogus" message="b" source="Lone"/>
</file>
</checkstyle>`)
	report, err := New().Parse(stdout, nil, 0)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []linter.Finding{
		{Path: "/workspace/Solution.java", Line: 3, Severity: linter.SeverityWarning, Rule: "Foo", Message: "w"},
		{Path: "/workspace/Solution.java", Line: 4, Severity: linter.SeverityInfo, Rule: "Bar", Message: "i"},
		{Path: "/workspace/Solution.java", Line: 5, Severity: linter.SeverityInfo, Message: "g"},
		{Path: "/workspace/Solution.java", Line: 6, Severity: linter.SeverityWarning, Rule: "Lone", Message: "b"},
	}
	if !slices.Equal(report.Findings, want) {
		t.Errorf("findings = %+v, want %+v", report.Findings, want)
	}
}

func TestRuleFromSource(t *testing.T) {
	cases := map[string]string{
		"com.puppycrawl.tools.checkstyle.checks.imports.AvoidStarImportCheck": "AvoidStarImport",
		"com.puppycrawl.tools.checkstyle.checks.sizes.LineLengthCheck":        "LineLength",
		"NoDotsCheck": "NoDots",
		"NoSuffix":    "NoSuffix",
		"":            "",
	}
	for in, want := range cases {
		if got := ruleFromSource(in); got != want {
			t.Errorf("ruleFromSource(%q) = %q, want %q", in, got, want)
		}
	}
}
