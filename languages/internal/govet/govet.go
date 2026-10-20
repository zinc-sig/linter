// Package govet is the `go vet` runner and diagnostics parser behind the
// go language. The Go toolchain release is pinned in
// languages/manifest.yaml; it fixes both the vet binary and the Go
// language version its typechecker assumes for bare files.
package govet

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/zinc-sig/linter/linter"
)

// ToolID is the stable tool identifier stamped into reports (contract
// §2) and, by construction, the driver id manifest.yaml stanzas select.
const ToolID = "go vet"

// diagRE matches vet diagnostics: path:line[:col]: message
var diagRE = regexp.MustCompile(`^([^\s:][^:]*\.go):(\d+)(?::(\d+))?: (.+)$`)

// Linter is a `go vet`-backed implementation of linter.Linter,
// parameterized by manifest language key and display name.
type Linter struct {
	language string
	name     string
}

// New returns a `go vet` linter for the given language key and display
// name.
func New(language, name string) *Linter {
	return &Linter{language: language, name: name}
}

func (l *Linter) Language() string { return l.language }
func (l *Linter) Name() string     { return l.name }

// Command passes every file to a single `go vet` invocation: bare .go files
// are compiled together as one "command-line-arguments" package, which
// matches how the workspace is staged. Files from mixed packages make the
// tool itself complain, and that surfaces as an operational failure — so no
// per-file looping is needed.
func (l *Linter) Command(files []string) []string {
	return append([]string{"go", "vet"}, files...)
}

// Env supplies defaults because bare-file `go vet` needs a writable build
// cache and a GOPATH even outside any module, and the container may run
// without a usable $HOME — so both default to /tmp. GOTOOLCHAIN=local pins
// the baked-in toolchain and GOPROXY=off guarantees no network access.
// GOMAXPROCS bounds the thread count so concurrent lints stay inside the
// container's pids limit.
// Variables already present in the environment take precedence.
func (l *Linter) Env() []string {
	return []string{
		"GOCACHE=/tmp/cobe-gocache",
		"GOPATH=/tmp/cobe-gopath",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		// GOMAXPROCS caps the threads of the go command AND every subprocess
		// it spawns (compile, the vet tool — env is inherited): the pinned
		// toolchain's runtime is not cgroup-aware, so on a many-core host each
		// subprocess otherwise spins host-core-count threads inside the
		// container's small CPU quota and pids limit — concurrent lints then
		// exhaust the pids limit and die with fork/exec EAGAIN.
		"GOMAXPROCS=2",
	}
}

func (l *Linter) Parse(stdout, stderr []byte, exitCode int) (linter.Report, error) {
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
		Language: l.language,
		Tool:     linter.ToolVersion("go vet", `go version (\S+)`, "go", "version"),
		ToolID:   ToolID,
		Findings: findings,
	}, nil
}
