// Package javac is the javac runner and report parser behind the java
// language. javac runs in the image's jlink'ed Java runtime through the
// javac worker (internal/javacworker/java), which checks a file as javac
// --release <N> would, up to flow analysis and without writing classes;
// the stanza's with.release picks the Java SE version.
package javac

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/zinc-sig/linter/linter"
)

// ToolID is the stable tool identifier stamped into reports (contract
// §2) and, by construction, the driver id manifest.yaml stanzas select.
const ToolID = "javac"

const (
	// javaPath is the jlink'ed minimal runtime holding the compiler (see
	// the Dockerfile's jre stage); the image ships no full JDK.
	javaPath  = "/opt/java/bin/java"
	workerJar = "/opt/javac-worker.jar"
)

// severities are the values the worker writes, all contract severities.
var severities = []string{linter.SeverityError, linter.SeverityWarning}

// Linter is a javac-backed implementation of linter.Linter, parameterized
// by manifest language key, display name, and Java SE release.
type Linter struct {
	language string
	name     string
	release  string
}

// New returns a javac linter for the given language key, display name and
// release (a --release value such as "11").
func New(language, name, release string) *Linter {
	return &Linter{language: language, name: name, release: release}
}

func (l *Linter) Language() string { return l.language }
func (l *Linter) Name() string     { return l.name }

// Release is the Java SE version the language is checked against; serve
// passes it to the resident worker with each request.
func (l *Linter) Release() string { return l.release }

// Command runs the worker for one check. It compiles the files together
// with every other .java file under the working directory, so provided
// sources resolve as they do when the submission is graded, and reports
// the named files only.
func (l *Linter) Command(files []string) []string {
	return append([]string{javaPath, "-XX:+UseSerialGC", "-XX:-UsePerfData", "-jar", workerJar,
		"--once", "--release", l.release}, files...)
}

// report is the worker's report object (Appendix A).
type report struct {
	Tool     string           `json:"tool"`
	Findings []linter.Finding `json:"findings"`
}

func (l *Linter) Parse(stdout, stderr []byte, exitCode int) (linter.Report, error) {
	// A compile error is a finding; a non-zero exit means the check itself
	// failed, such as an unreadable file or an unsupported release.
	if exitCode != 0 {
		return linter.Report{}, fmt.Errorf("javac worker exited %d\nstderr: %s", exitCode, linter.StderrSnippet(stderr))
	}
	var r report
	if err := json.Unmarshal(stdout, &r); err != nil {
		return linter.Report{}, fmt.Errorf("javac worker: unparseable report: %v", err)
	}
	if r.Findings == nil {
		return linter.Report{}, fmt.Errorf("javac worker: report has no findings array")
	}
	for _, f := range r.Findings {
		if !slices.Contains(severities, f.Severity) {
			return linter.Report{}, fmt.Errorf("javac worker: finding with severity %q", f.Severity)
		}
	}
	return linter.Report{
		Version:  linter.ReportVersion,
		Language: l.language,
		Tool:     r.Tool,
		ToolID:   ToolID,
		Findings: r.Findings,
	}, nil
}
