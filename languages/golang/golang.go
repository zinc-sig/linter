// Package golang lints Go sources with `go vet` (manifest key "go").
package golang

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/zinc-sig/linter/linter"
)

// GoVersion is the Go toolchain release installed into the image;
// cmd/toolversions feeds it to the Dockerfile build. It pins both the vet
// binary and the Go language version its typechecker assumes for bare files.
const GoVersion = "1.24.0"

// diagRE matches vet diagnostics: path:line[:col]: message
var diagRE = regexp.MustCompile(`^([^\s:][^:]*\.go):(\d+)(?::(\d+))?: (.+)$`)

type govet struct{}

// New returns the go language implementation.
func New() linter.Linter { return govet{} }

func (govet) Language() string { return "go" }

// Name is the display name served to UI/API surfaces.
func (govet) Name() string { return "Go" }

// Command passes every file to a single `go vet` invocation: bare .go files
// are compiled together as one "command-line-arguments" package, which
// matches how the workspace is staged. Files from mixed packages make the
// tool itself complain, and that surfaces as an operational failure — so no
// per-file looping is needed.
func (govet) Command(files []string) []string {
	return append([]string{"go", "vet"}, files...)
}

// Env supplies defaults because bare-file `go vet` needs a writable build
// cache and a GOPATH even outside any module, and the container may run
// without a usable $HOME — so both default to /tmp. GOTOOLCHAIN=local pins
// the baked-in toolchain and GOPROXY=off guarantees no network access.
// Variables already present in the environment take precedence.
func (govet) Env() []string {
	return []string{
		"GOCACHE=/tmp/cobe-gocache",
		"GOPATH=/tmp/cobe-gopath",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
	}
}

func (govet) Parse(stdout, stderr []byte, exitCode int) (linter.Report, error) {
	findings := []linter.Finding{}
	for _, raw := range strings.Split(string(stderr), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			// package headers such as "# command-line-arguments"
			continue
		}
		severity := linter.SeverityWarning
		if rest, ok := strings.CutPrefix(line, "vet: "); ok {
			// typecheck/compile failures reported by vet
			line = rest
			severity = linter.SeverityError
		}
		m := diagRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNo, _ := strconv.Atoi(m[2])
		column := 0
		if m[3] != "" {
			column, _ = strconv.Atoi(m[3])
		}
		findings = append(findings, linter.Finding{
			Path:     m[1],
			Line:     lineNo,
			Column:   column,
			Severity: severity,
			Message:  m[4],
		})
	}

	// vet exits non-zero whenever it reported diagnostics; that is data.
	if exitCode != 0 && len(findings) == 0 {
		return linter.Report{}, fmt.Errorf("go vet: exit %d with no parseable diagnostics\nstderr: %s", exitCode, linter.StderrSnippet(stderr))
	}

	return linter.Report{
		Version:  linter.ReportVersion,
		Language: "go",
		Tool:     linter.ToolVersion("go vet", `go version (\S+)`, "go", "version"),
		Findings: findings,
	}, nil
}
