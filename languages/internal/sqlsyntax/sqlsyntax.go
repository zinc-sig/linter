// Package sqlsyntax is the runner and JSON parser behind the SQL
// languages. The sqlsyntax command (tools/sqlsyntax) reports syntax
// errors for one SQL dialect per run: it parses PostgreSQL with that
// server's own parser and MySQL with the TiDB parser. Its parser releases
// are pinned in tools/sqlsyntax/go.mod. No database is consulted, so an
// unknown table or column is not a finding.
package sqlsyntax

import (
	"encoding/json"
	"fmt"

	"github.com/zinc-sig/linter/linter"
)

// ToolID is the stable tool identifier stamped into reports (contract
// §2) and, by construction, the driver id manifest.yaml stanzas select.
const ToolID = "sqlsyntax"

// BinPath is the stable path the Dockerfile installs the command at.
const BinPath = "/usr/local/bin/sqlsyntax"

// Rule is the rule id of every finding: the command reports syntax
// errors and nothing else.
const Rule = "syntax-error"

// Dialects are the --dialect values the command accepts.
var Dialects = []string{"mysql", "postgresql"}

// output is the document the command prints.
type output struct {
	Diagnostics []struct {
		File      string `json:"file"`
		Line      int    `json:"line"`
		Column    int    `json:"column"`
		EndLine   int    `json:"end_line"`
		EndColumn int    `json:"end_column"`
		Message   string `json:"message"`
	} `json:"diagnostics"`
}

// Linter is a sqlsyntax-backed implementation of linter.Linter,
// parameterized by manifest language key, display name, and dialect.
type Linter struct {
	language string
	name     string
	dialect  string
}

// New returns a linter for the given language key and display name that
// checks sources against dialect, one of Dialects.
func New(language, name, dialect string) *Linter {
	return &Linter{language: language, name: name, dialect: dialect}
}

func (l *Linter) Language() string { return l.language }
func (l *Linter) Name() string     { return l.name }

// Command passes every file to one invocation; findings carry per-file
// paths.
func (l *Linter) Command(files []string) []string {
	return append([]string{BinPath, "--dialect", l.dialect}, files...)
}

// Parse follows the command's exit codes: 0 = clean, 1 = syntax errors
// reported (data), anything else = it could not run (operational
// failure).
func (l *Linter) Parse(stdout, stderr []byte, exitCode int) (linter.Report, error) {
	if exitCode != 0 && exitCode != 1 {
		return linter.Report{}, fmt.Errorf("sqlsyntax: could not run (exit %d): %s", exitCode, linter.StderrSnippet(stderr))
	}
	var doc output
	if err := json.Unmarshal(stdout, &doc); err != nil {
		return linter.Report{}, fmt.Errorf("sqlsyntax: unparseable JSON output: %v\nstderr: %s", err, linter.StderrSnippet(stderr))
	}

	findings := make([]linter.Finding, 0, len(doc.Diagnostics))
	for _, d := range doc.Diagnostics {
		findings = append(findings, linter.Finding{
			Path:      d.File,
			Line:      max(d.Line, 0),
			Column:    max(d.Column, 0),
			EndLine:   max(d.EndLine, 0),
			EndColumn: max(d.EndColumn, 0),
			Severity:  linter.SeverityError,
			Rule:      Rule,
			Message:   d.Message,
		})
	}

	return linter.Report{
		Version:  linter.ReportVersion,
		Language: l.language,
		Tool:     linter.ToolVersion("sqlsyntax", `sqlsyntax (.+)`, BinPath, "--version"),
		ToolID:   ToolID,
		Findings: findings,
	}, nil
}
