package cpp

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zinc-sig/linter/linter"
)

// loadCase reads a captured native-tool run from testdata: real clang-tidy
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
	if l.Language() != "cpp" {
		t.Errorf("Language = %q", l.Language())
	}
	if l.Filename() != "solution.cpp" {
		t.Errorf("Filename = %q", l.Filename())
	}
}

func TestParseDirty(t *testing.T) {
	report, err := New().Parse(loadCase(t, "dirty"))
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
	if report.Language != "cpp" {
		t.Errorf("language = %q", report.Language)
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
