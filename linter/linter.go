// Package linter defines the unified findings schema shared by every
// language implementation, the Linter interface a language must satisfy,
// and the exec helper that runs a native tool and normalizes its results.
//
// See docs/CONTRACT.md for the authoritative wire contract.
package linter

// ReportVersion is the current output-schema version (contract §2).
const ReportVersion = 1

// Severity values permitted by the contract (§2).
const (
	SeverityError      = "error"
	SeverityWarning    = "warning"
	SeverityConvention = "convention"
	SeverityRefactor   = "refactor"
	SeverityInfo       = "info"
)

// Finding is a single normalized diagnostic (contract §2).
type Finding struct {
	// Path is the workspace-relative path of the offending file, matching
	// the path the CLI was invoked with.
	Path string `json:"path"`
	// Line is 1-based; 0 when unknown or file-scoped.
	Line int `json:"line"`
	// Column is 1-based; omitted when unknown.
	Column int `json:"column,omitempty"`
	// Severity is one of the Severity* constants.
	Severity string `json:"severity"`
	// Rule is the tool-native rule/check id, when the tool has one.
	Rule string `json:"rule,omitempty"`
	// Message is the human-readable description.
	Message string `json:"message"`
}

// Report is the unified JSON document written to stdout (contract §2).
type Report struct {
	Version  int       `json:"version"`
	Language string    `json:"language"`
	Tool     string    `json:"tool"`
	Findings []Finding `json:"findings"`
}

// Linter is implemented once per language, in languages/<lang>/.
type Linter interface {
	// Language is the manifest key, e.g. "python".
	Language() string
	// Filename is the name the student's file is staged under inside the
	// workspace, e.g. "solution.py".
	Filename() string
	// Command returns the native tool argv for linting files. No shell is
	// involved; the argv is exec'd as-is.
	Command(files []string) []string
	// Parse converts the native tool's output into a Report. It returns an
	// error ONLY for operational failures (tool crashed, unparseable
	// output): a non-zero exitCode whose output still parses into findings
	// is data, not failure (contract §3).
	Parse(stdout, stderr []byte, exitCode int) (Report, error)
}

// Enver is optionally implemented by linters whose native tool needs
// default environment variables. Entries are "KEY=VALUE" and are applied
// only when KEY is not already set in the process environment.
type Enver interface {
	Env() []string
}
