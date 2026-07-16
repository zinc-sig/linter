# cobe-linter ⇄ core contract (v1)

> The authoritative copy of this contract now lives in
> [zinc-sig/core](https://github.com/zinc-sig/core) at
> `sdk/lintcontract/CONTRACT.md`, alongside core's wire types, parsers, and
> golden vectors. This document is kept in sync as the reference
> implementation's copy.

This document describes the interface between the
linter image and [zinc-sig/core](https://github.com/zinc-sig/core). Core
contains **no per-language code**: the set of supported languages, how each
linter is invoked, and how its output is normalized all live in this image
behind a single CLI, `/usr/local/bin/cobe-lint`. A system operator adds a
language by forking this repo — core picks it up automatically.

## 1. Language manifest — `cobe-lint manifest`

Core discovers the languages by exec'ing `/usr/local/bin/cobe-lint manifest`
in the linter container at startup and derives everything from the JSON it
writes to stdout: the supported-language set (also served via core's public
"supported languages" endpoint) and the lint command to exec per language.

```json
{
  "version": 1,
  "languages": {
    "python313": {
      "name": "Python 3.13",
      "command": ["/usr/local/bin/cobe-lint", "lint", "python313"]
    }
  }
}
```

- `version` (int, required): contract version. Core rejects manifests whose
  major version it does not know.
- `languages` (object, required): key = language identifier (lowercase,
  `[a-z0-9_+-]+`), value:
  - `name` (string, required): human-readable display name for UI/API
    surfaces (e.g. `Python 3.13`); the map key remains the stable
    identifier.
  - `command` (array of strings, required): argv **prefix** to exec inside
    the linter container. The caller appends one or more workspace-relative
    file paths as trailing arguments — there are no placeholders. No shell is
    involved; the argv is exec'd as-is. All files of one lint run go to a
    single invocation, and findings carry per-file paths.

The workspace filename each language lints under is owned and configured by
core (deployment config), not by this image.

## 2. Unified findings output

Every manifest `command` MUST write a single JSON document to **stdout**:

```json
{
  "version": 1,
  "language": "python313",
  "tool": "ruff 0.15.21",
  "findings": [
    {
      "path": "solution.py",
      "line": 3,
      "column": 8,
      "severity": "warning",
      "rule": "F401",
      "message": "`os` imported but unused"
    }
  ]
}
```

- `version` (int, required): output-schema version, currently `1`.
- `language` (string, required): the manifest key that was invoked.
- `tool` (string, required): freeform tool name + version, for display and
  debugging. Consumers must not key behavior on it — use `tool_id`.
- `tool_id` (string, optional, additive in v1): stable tool identifier
  (e.g. `ruff`, `checkstyle`, `clang-tidy`, `go vet`) that consumers may
  key icons/filters/suppressions on. When absent, core falls back to a
  prefix match of `tool` against known ids.
- `findings` (array, required, may be empty):
  - `path` (string, required): path as given on the command line
    (workspace-relative).
  - `line` (int, required): 1-based line; `0` when unknown/file-scoped.
  - `column` (int, optional): 1-based column; `0`/absent when unknown.
  - `end_line`, `end_column` (int, optional, additive in v1): 1-based
    inclusive end of the flagged range; `0`/absent when unknown, in which
    case consumers treat the range as collapsed to the start position.
  - `severity` (string, required): one of `error`, `warning`, `convention`,
    `refactor`, `info`. Tool-native categories are mapped by the language
    implementation (e.g. ruff: syntax errors and undefined names
    (`F821`-`F823`)→`error`, other pyflakes `F` and pycodestyle
    `W`→`warning`, pycodestyle `E`→`convention`; checkstyle
    `error`→`error`, `warning`→`warning`, `info`→`info`).
  - `rule` (string, optional): tool-native rule/check id (e.g. `F401`,
    `AvoidStarImport`, `clang-analyzer-core.NullDereference`).
  - `message` (string, required): human-readable description.

## 3. Exit-code semantics

- **`0`** — the lint ran to completion. Findings (including zero findings) are
  in the JSON on stdout. A lint that found errors in the student's code still
  exits `0`: findings are data, not failure.
- **non-zero** — operational failure (tool crashed, unparseable output, bad
  arguments, unknown language, unreadable file). Stdout may be empty or
  partial; the CLI writes a diagnostic to stderr. Core reports this as a
  lint-execution error, not as findings.
- Where the boundary sits follows the native tool: a Java source Checkstyle
  cannot parse at all makes it crash without a report (non-zero), while a
  Python syntax error is an ordinary ruff `invalid-syntax` finding (exit `0`).

## 4. Invocation environment

- Working directory: a core-owned workspace directory prepared per invocation,
  containing the student file (named per the instructor's question
  configuration) alongside any sibling assets (headers, provided sources).
  File paths passed to `cobe-lint` are relative to that directory; linters may
  resolve sibling files through the working directory. The image's
  `/workspace` merely guarantees a writable default location.
- The container runs as the non-root `linter` user; commands must not require
  root.
- Core enforces its own execution timeout and output-size caps outside the
  container; the CLI needs no timeout logic.

## 5. Compatibility rules

- Adding a language, or adding optional finding fields, is backward-compatible
  within version 1.
- Renaming/removing manifest fields, changing severity values, or changing
  exit-code semantics requires bumping `version` in both manifest and output.

## 6. Adding a language

Implement the `linter.Linter` interface (`Language`, `Name`, `Command`,
`Parse`) in
a new `languages/<lang>/` package and register it in
`languages/languages.go` — the manifest, the CLI, and the conformance tests
all derive from that registry automatically. No filename is involved: the
workspace filename for the new language is configured on core's side as
deployment config.
