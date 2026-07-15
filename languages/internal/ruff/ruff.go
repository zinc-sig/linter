// Package ruff is the shared ruff runner and JSON parser behind the
// python<NN> language implementations. One pinned native ruff binary lints
// every Python language line; each implementation selects its dialect via
// ruff's --target-version flag, so no per-version interpreters are baked
// into the image.
package ruff

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zinc-sig/linter/linter"
)

// Version is the ruff release baked into the image, shared by every
// python<NN> package; cmd/toolversions feeds it to the Dockerfile build
// via the packages' RuffVersion re-exports (this internal package is
// outside cmd's import range).
const Version = "0.15.21"

// BinPath is the stable path the Dockerfile installs the ruff binary at.
const BinPath = "/usr/local/bin/ruff"

// diagnostic is the subset of ruff's --output-format=json array we
// consume. Code is a pointer because ruff has emitted null for syntax
// errors in past releases (current ones use "invalid-syntax").
type diagnostic struct {
	Code     *string  `json:"code"`
	Message  string   `json:"message"`
	Filename string   `json:"filename"`
	Location position `json:"location"`
}

// position is 1-based in ruff's output, matching the contract.
type position struct {
	Row    int `json:"row"`
	Column int `json:"column"`
}

// severity maps a ruff rule code onto the contract enum. Clients surface
// only "error" findings to students, so that tier is reserved for what
// must stop them: syntax errors (code null or "invalid-syntax" depending
// on the ruff release, "E9xx" historically) and pyflakes' undefined names
// (F821-F823). The rest of pyflakes (F) reports real-but-runnable code
// smells — warning; pycodestyle errors (E) are style — convention;
// pycodestyle warnings (W) and any code a future config might enable
// default to warning.
func severity(code string) string {
	switch {
	case code == "" || code == "invalid-syntax" || strings.HasPrefix(code, "E9"):
		return linter.SeverityError
	case code == "F821" || code == "F822" || code == "F823":
		return linter.SeverityError
	case strings.HasPrefix(code, "E"):
		return linter.SeverityConvention
	default:
		return linter.SeverityWarning
	}
}

// Linter is a ruff-backed implementation of linter.Linter, parameterized
// by manifest language key, display name, and ruff --target-version value.
type Linter struct {
	language string
	name     string
	target   string
}

// New returns a ruff linter for the given language key and display name,
// checking sources against the Python dialect named by target (a ruff
// --target-version value such as "py313").
func New(language, name, target string) *Linter {
	return &Linter{language: language, name: name, target: target}
}

func (l *Linter) Language() string { return l.language }
func (l *Linter) Name() string     { return l.name }

// Command passes every file to one ruff invocation; findings carry
// per-file paths. --no-cache is required, not an optimization: the
// workspace is not writable by the container's linter user, and ruff
// aborts (exit 2) when it cannot create .ruff_cache there.
func (l *Linter) Command(files []string) []string {
	return append([]string{BinPath, "check", "--no-cache", "--output-format=json", "--target-version", l.target}, files...)
}

// Parse follows ruff's exit-code contract: 0 = clean, 1 = violations
// reported (data — including syntax errors, which ruff emits as ordinary
// diagnostics), 2 = usage or internal error (operational failure).
func (l *Linter) Parse(stdout, stderr []byte, exitCode int) (linter.Report, error) {
	if exitCode != 0 && exitCode != 1 {
		return linter.Report{}, fmt.Errorf("ruff: usage or internal error (exit %d): %s", exitCode, linter.StderrSnippet(stderr))
	}
	var diagnostics []diagnostic
	if err := json.Unmarshal(stdout, &diagnostics); err != nil {
		return linter.Report{}, fmt.Errorf("ruff: unparseable JSON output: %v\nstderr: %s", err, linter.StderrSnippet(stderr))
	}

	findings := make([]linter.Finding, 0, len(diagnostics))
	for _, d := range diagnostics {
		code := ""
		if d.Code != nil {
			code = *d.Code
		}
		findings = append(findings, linter.Finding{
			// ruff reports absolute paths; linter.Run maps them back to
			// the paths given on the command line.
			Path:     d.Filename,
			Line:     max(d.Location.Row, 0),
			Column:   max(d.Location.Column, 0),
			Severity: severity(code),
			Rule:     code,
			Message:  d.Message,
		})
	}

	return linter.Report{
		Version:  linter.ReportVersion,
		Language: l.language,
		Tool:     linter.ToolVersion("ruff", `ruff (\S+)`, BinPath, "--version"),
		Findings: findings,
	}, nil
}
