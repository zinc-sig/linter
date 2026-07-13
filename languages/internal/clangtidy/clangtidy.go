// Package clangtidy is the shared clang-tidy runner and diagnostics parser
// behind the c and cpp language implementations.
package clangtidy

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"

	"github.com/zinc-sig/linter/linter"
)

// diagRE matches clang-tidy diagnostic lines:
//
//	path:line:col: severity: message [rule]
//
// The indented source-context and caret lines that follow each diagnostic
// never match (they cannot start a path).
var diagRE = regexp.MustCompile(`^([^\s:][^:]*):(\d+):(\d+): (error|warning|remark|note): (.*?)( \[([^\[\]]+)\])?$`)

// severityByName maps clang severities onto the contract enum. "note"
// deliberately has no entry: note lines annotate the preceding diagnostic
// (macro expansions, analyzer steps) rather than reporting a new finding,
// so they are skipped.
var severityByName = map[string]string{
	"error":   linter.SeverityError,
	"warning": linter.SeverityWarning,
	"remark":  linter.SeverityInfo,
}

// Linter is a clang-tidy-backed implementation of linter.Linter,
// parameterized by manifest language key and the -std= language standard.
type Linter struct {
	language string
	std      string
}

// New returns a clang-tidy linter for the given language key, checking
// against the given language standard (e.g. "gnu17", "gnu++17").
func New(language, std string) *Linter {
	return &Linter{language: language, std: std}
}

func (l *Linter) Language() string { return l.language }

func (l *Linter) Command(files []string) []string {
	// The "--" compiles with default flags instead of looking for a
	// compilation database; the explicit -std pins the language standard.
	return append(append([]string{"clang-tidy"}, files...), "--", "-std="+l.std)
}

func (l *Linter) Parse(stdout, stderr []byte, exitCode int) (linter.Report, error) {
	findings := []linter.Finding{}
	for _, stream := range [][]byte{stdout, stderr} {
		for _, raw := range bytes.Split(stream, []byte("\n")) {
			m := diagRE.FindStringSubmatch(string(raw))
			if m == nil {
				continue
			}
			severity, ok := severityByName[m[4]]
			if !ok {
				continue
			}
			findings = append(findings, linter.Finding{
				Path:     m[1],
				Line:     atoi(m[2]),
				Column:   atoi(m[3]),
				Severity: severity,
				Rule:     m[7],
				Message:  m[5],
			})
		}
	}

	// clang-tidy exits non-zero when it emits error-severity diagnostics —
	// e.g. compile errors in the student's code — and those are findings,
	// not failures. Only a non-zero exit with nothing parseable means the
	// tool itself failed.
	if exitCode != 0 && len(findings) == 0 {
		return linter.Report{}, fmt.Errorf("clang-tidy: exit %d with no parseable diagnostics\nstderr: %s", exitCode, linter.StderrSnippet(stderr))
	}

	return linter.Report{
		Version:  linter.ReportVersion,
		Language: l.language,
		Tool:     linter.ToolVersion("clang-tidy", `LLVM version (\S+)`, "clang-tidy", "--version"),
		Findings: findings,
	}, nil
}

// atoi converts a regexp-validated digit string.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
