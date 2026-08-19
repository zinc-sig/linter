# Adding a language: a worked example

This guide walks through adding a linter to the image from scratch, using
**shellcheck for shell scripts** as the running example. Nothing here is
registered in the repo — it is a template to copy.

A language is a stanza in [`languages/manifest.yaml`](../languages/manifest.yaml)
binding a stable key and display name to a **tool driver**. When the tool
already has a driver (ruff, clang-tidy, checkstyle, go vet), adding a
language is *only* a stanza — e.g. python314 would be:

```yaml
python314:
  name: Python 3.14
  tool: ruff
  target: py314
```

plus the language tables in `languages/languages_test.go` (end of step 5)
and conformance samples (step 6). Shellcheck is a new tool, so the full
path is:

1. [Implement the tool driver](#1-implement-the-tool-driver) in `languages/internal/shellcheck/`
2. [Bind the driver](#2-bind-the-driver) in `languages/languages.go`
3. [Declare the language and pin](#3-declare-the-language-and-pin-in-the-manifest) in `languages/manifest.yaml`
4. [Install the tool](#4-install-the-tool-in-the-dockerfile) in the Dockerfile
5. [Unit-test `Parse`](#5-unit-test-parse-on-inline-fixtures) on inline fixtures
6. [Add conformance samples](#6-add-conformance-samples)
7. [Run the gate](#7-run-the-gate)

Two things are deliberately **not** part of this repo's job:

- **The workspace filename** (e.g. `solution.sh`) — core owns it as
  deployment config; you configure it on core's side when enabling the
  language.
- **Core code changes** — there are none. Core discovers the new language
  from `cobe-lint manifest` automatically (see
  [`CONTRACT.md`](CONTRACT.md) §1 and §6).

## 0. Know your tool first

Before writing code, answer for your linter what the existing drivers
answer in their package docs:

- **Machine-readable output?** shellcheck has `--format=json1`.
- **Exit codes?** shellcheck exits non-zero when it *finds issues* — per
  contract §3 that is still data (CLI exit `0`); only crashes/unparseable
  output are operational failures.
- **Severity levels?** shellcheck: `error`, `warning`, `info`, `style` —
  they must map onto the contract enum (`error`, `warning`, `convention`,
  `refactor`, `info`).
- **Multiple files in one invocation?** shellcheck: yes (if your tool can
  only take one file, loop inside your driver and merge the findings into
  one Report, documenting why).
- **Version probing?** `shellcheck --version` prints `version: 0.10.0`.

## 1. Implement the tool driver

Create `languages/internal/shellcheck/shellcheck.go`. A driver is
parameterized by the manifest language key and display name (plus any
per-language options it declares — compare ruff's `target` and
clang-tidy's `std`), so several manifest stanzas can share it:

```go
// Package shellcheck is the shellcheck runner and JSON parser behind the
// shell language. The shellcheck release is pinned in
// languages/manifest.yaml; the shell dialect linted is decided per file
// by its shebang (shellcheck's default).
package shellcheck

import (
	"encoding/json"
	"fmt"

	"github.com/zinc-sig/linter/linter"
)

// severityByLevel maps shellcheck levels onto the contract enum.
var severityByLevel = map[string]string{
	"error":   linter.SeverityError,
	"warning": linter.SeverityWarning,
	"info":    linter.SeverityInfo,
	"style":   linter.SeverityConvention,
}

// output is the subset of shellcheck's --format=json1 document we consume.
type output struct {
	Comments []struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Column  int    `json:"column"`
		Level   string `json:"level"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"comments"`
}

// Linter is a shellcheck-backed implementation of linter.Linter,
// parameterized by manifest language key and display name.
type Linter struct {
	language string
	name     string
}

// New returns a shellcheck linter for the given language key and display
// name.
func New(language, name string) *Linter {
	return &Linter{language: language, name: name}
}

func (l *Linter) Language() string { return l.language }
func (l *Linter) Name() string     { return l.name }

// Command passes every file to one shellcheck invocation; findings carry
// per-file paths.
func (l *Linter) Command(files []string) []string {
	return append([]string{"shellcheck", "--format=json1"}, files...)
}

func (l *Linter) Parse(stdout, stderr []byte, exitCode int) (linter.Report, error) {
	// shellcheck exits 1 when it reports comments — that is data, not
	// failure. Whatever the exit code, a parseable JSON document decides.
	var doc output
	if err := json.Unmarshal(stdout, &doc); err != nil {
		return linter.Report{}, fmt.Errorf("shellcheck: unparseable JSON output (exit %d): %v\nstderr: %s",
			exitCode, err, linter.StderrSnippet(stderr))
	}

	findings := make([]linter.Finding, 0, len(doc.Comments))
	for _, c := range doc.Comments {
		severity, ok := severityByLevel[c.Level]
		if !ok {
			severity = linter.SeverityWarning
		}
		findings = append(findings, linter.Finding{
			Path:     c.File,
			Line:     max(c.Line, 0),
			Column:   max(c.Column, 0),
			Severity: severity,
			Rule:     fmt.Sprintf("SC%d", c.Code),
			Message:  c.Message,
		})
	}

	return linter.Report{
		Version:  linter.ReportVersion,
		Language: l.language,
		Tool:     linter.ToolVersion("shellcheck", `version:\s+(\S+)`, "shellcheck", "--version"),
		ToolID:   "shellcheck",
		Findings: findings,
	}, nil
}
```

Notes:

- `Parse` returns an error **only** for operational failures; findings —
  even errors in the student's script — come back with a `nil` error and
  the CLI exits `0` (contract §3).
- Paths are reported verbatim; `linter.Run` normalizes them back to the
  paths given on the command line.
- If your tool needs environment defaults (caches, offline switches),
  implement the optional `linter.Enver` interface — see
  `languages/internal/govet/govet.go` for a commented example.

## 2. Bind the driver

Teach the registry's `build` switch in
[`languages/languages.go`](../languages/languages.go) about the new
driver id. The id is what `tool:` stanzas name, and by convention matches
the `ToolID` the driver stamps into reports:

```go
case "shellcheck":
	if l.Target != "" || l.Std != "" {
		return nil, fmt.Errorf("tool shellcheck takes no options")
	}
	return shellcheck.New(key, l.Name), nil
```

A driver that needs a per-language option validates it here instead, the
way ruff requires `target` and clang-tidy requires `std` — a manifest typo
must fail at load, not at lint time.

## 3. Declare the language and pin in the manifest

In [`languages/manifest.yaml`](../languages/manifest.yaml):

```yaml
languages:
  shell:
    name: Shell
    tool: shellcheck

tools:
  shellcheck: "0.10.0"
```

The `tools:` entry **is** the version pin. Add its shell variable to the
ordered list in `cmd/toolversions/main.go` so the Dockerfile can consume
it:

```go
{"shellcheck", "SHELLCHECK_VERSION"},
```

Tools that come from Debian's repositories (like clang-tidy) skip the pin
entirely — the base-image pin determines their version; say so in a
manifest comment instead.

## 4. Install the tool in the Dockerfile

In the runtime stage, sourcing the generated pins (the release tarball
needs `xz-utils` added to the apt install list):

```dockerfile
ARG TARGETARCH
# hadolint ignore=SC1091
RUN . /opt/tool-versions.sh \
    && case "${TARGETARCH}" in amd64) arch=x86_64 ;; arm64) arch=aarch64 ;; esac \
    && curl -fsSL "https://github.com/koalaman/shellcheck/releases/download/v${SHELLCHECK_VERSION}/shellcheck-v${SHELLCHECK_VERSION}.linux.${arch}.tar.xz" \
    | tar -xJ --strip-components=1 -C /usr/local/bin "shellcheck-v${SHELLCHECK_VERSION}/shellcheck"
```

(Alternatively `apt-get install shellcheck` — then, as with clang-tidy,
drop the pin and document that Debian determines the version.)

`cobe-lint manifest` now advertises
`"shell": {"name": "Shell", "command": ["/usr/local/bin/cobe-lint", "lint", "shell"]}`
and the conformance suite will demand fixtures for it.

## 5. Unit-test Parse on inline fixtures

Capture real tool output once (`docker run --rm -v "$PWD/dirty.sh:/workspace/dirty.sh:ro" -w /workspace <image> shellcheck --format=json1 dirty.sh; echo $?`)
and inline it as consts in
`languages/internal/shellcheck/shellcheck_test.go`:

```go
package shellcheck

import (
	"testing"

	"github.com/zinc-sig/linter/linter"
)

// newTest builds the driver exactly as the shell manifest stanza does.
func newTest() *Linter { return New("shell", "Shell") }

// Inline fixture: real shellcheck --format=json1 output captured from the
// image; dirtyExitCode is the recorded exit status of that run.
const dirtyExitCode = 1

const dirtyStdout = `{"comments":[{"file":"dirty.sh","line":2,"endLine":2,"column":1,"endColumn":7,"level":"warning","code":2034,"message":"unused appears unused. Verify use (or export if used externally).","fix":null}]}`

func TestParseDirty(t *testing.T) {
	report, err := newTest().Parse([]byte(dirtyStdout), nil, dirtyExitCode)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %+v, want 1", report.Findings)
	}
	got := report.Findings[0]
	want := linter.Finding{
		Path: "dirty.sh", Line: 2, Column: 1,
		Severity: linter.SeverityWarning, Rule: "SC2034",
		Message: "unused appears unused. Verify use (or export if used externally).",
	}
	if got != want {
		t.Errorf("finding = %+v, want %+v", got, want)
	}
}

func TestParseClean(t *testing.T) {
	report, err := newTest().Parse([]byte(`{"comments":[]}`), nil, 0)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if report.Findings == nil || len(report.Findings) != 0 {
		t.Errorf("findings = %#v, want empty non-nil slice", report.Findings)
	}
}

func TestParseGarbageIsOperationalFailure(t *testing.T) {
	if _, err := newTest().Parse([]byte("not json"), nil, 2); err == nil {
		t.Fatal("Parse must fail on unparseable output")
	}
}
```

Also cover your tool's edge cases the way the existing drivers do:
severity fallbacks, missing columns, the exit-code boundary (compare
`languages/internal/ruff/ruff_test.go` and
`languages/internal/clangtidy/clangtidy_test.go`). Finally, register the
new language in `languages/languages_test.go`: its key in `TestRegistry`,
its display name in `TestNames`, and its exact native argv in
`TestNativeCommands` — that last table is what catches a manifest typo.

## 6. Add conformance samples

In `tests/conformance_test.go`, add consts and a `fixtures` map entry — the
suite drives every manifest language against them inside the built image:

```go
const shellClean = `#!/bin/sh
echo "hello"
`

// unused variable -> shellcheck SC2034
const shellDirty = `#!/bin/sh
unused="never read"
echo "hello"
`
```

```go
"shell": {
	"clean": {"solution.sh", shellClean},
	"dirty": {"solution.sh", shellDirty},
},
```

The dirty sample must trigger at least one finding; the clean one none.

## 7. Run the gate

```bash
gofmt -l . && go vet ./... && go test -count=1 ./...   # unit level
sudo docker run --rm -i hadolint/hadolint hadolint --ignore DL3008 - < Dockerfile
docker build -t cobe-linter:dev .
go test -tags conformance -count=1 ./tests/            # against the image
```

All green means core will pick the language up from the manifest with zero
core-side code changes — only its deployment config needs the new
language's workspace filename.
