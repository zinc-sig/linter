// Package python lints Python sources with pylint.
package python

import (
	"encoding/json"
	"fmt"

	"github.com/zinc-sig/linter/linter"
)

// pylint's exit status is a bit field: 1 fatal, 2 error, 4 warning,
// 8 refactor, 16 convention, 32 usage error. Message bits — including
// fatal — still come with parseable JSON findings and are data; only the
// usage-error bit is an operational failure.
const usageErrorBit = 32

var severityByPrefix = map[byte]string{
	'E': linter.SeverityError,
	'F': linter.SeverityError,
	'W': linter.SeverityWarning,
	'C': linter.SeverityConvention,
	'R': linter.SeverityRefactor,
	'I': linter.SeverityInfo,
}

var severityByType = map[string]string{
	"error":      linter.SeverityError,
	"fatal":      linter.SeverityError,
	"warning":    linter.SeverityWarning,
	"convention": linter.SeverityConvention,
	"refactor":   linter.SeverityRefactor,
	"info":       linter.SeverityInfo,
}

// message is the subset of pylint's JSON output we consume.
type message struct {
	Type      string `json:"type"`
	MessageID string `json:"message-id"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Message   string `json:"message"`
}

type pylint struct{}

// New returns the python language implementation.
func New() linter.Linter { return pylint{} }

func (pylint) Language() string { return "python" }
func (pylint) Filename() string { return "solution.py" }

func (pylint) Command(files []string) []string {
	return append([]string{"pylint", "--output-format=json", "--disable=C0114,C0115,C0116"}, files...)
}

func (pylint) Parse(stdout, stderr []byte, exitCode int) (linter.Report, error) {
	if exitCode&usageErrorBit != 0 {
		return linter.Report{}, fmt.Errorf("pylint: usage error (exit %d): %s", exitCode, linter.StderrSnippet(stderr))
	}
	var messages []message
	if err := json.Unmarshal(stdout, &messages); err != nil {
		return linter.Report{}, fmt.Errorf("pylint: unparseable JSON output: %v\nstderr: %s", err, linter.StderrSnippet(stderr))
	}

	findings := make([]linter.Finding, 0, len(messages))
	for _, m := range messages {
		severity := ""
		if m.MessageID != "" {
			severity = severityByPrefix[m.MessageID[0]]
		}
		if severity == "" {
			severity = severityByType[m.Type]
		}
		if severity == "" {
			severity = linter.SeverityWarning
		}
		column := 0
		if m.Column >= 0 {
			// pylint columns are 0-based; the contract is 1-based.
			column = m.Column + 1
		}
		findings = append(findings, linter.Finding{
			Path:     m.Path,
			Line:     max(m.Line, 0),
			Column:   column,
			Severity: severity,
			Rule:     m.MessageID,
			Message:  m.Message,
		})
	}

	return linter.Report{
		Version:  linter.ReportVersion,
		Language: "python",
		Tool:     linter.ToolVersion("pylint", `pylint\s+(\S+)`, "pylint", "--version"),
		Findings: findings,
	}, nil
}
