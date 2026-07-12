# cobe-linter ⇄ core contract (v1)

This document is the single source of truth for the interface between the
linter image and [zinc-sig/core](https://github.com/zinc-sig/core). Core
contains **no per-language code**: the set of supported languages, how each
linter is invoked, and how its output is normalized all live in this image
behind a single CLI, `/usr/local/bin/cobe-lint`. A system operator adds a
language by forking this repo — core picks it up automatically.

## 1. Language manifest — `cobe-lint manifest`

Core discovers the languages by exec'ing `/usr/local/bin/cobe-lint manifest`
in the linter container at startup and derives everything from the JSON it
writes to stdout: the supported-language set (also served via core's public
"supported languages" endpoint), the target filename per language, and the
lint command to exec.

```json
{
  "version": 1,
  "languages": {
    "python": {
      "filename": "solution.py",
      "command": ["/usr/local/bin/cobe-lint", "lint", "python"]
    }
  }
}
```

- `version` (int, required): contract version. Core rejects manifests whose
  major version it does not know.
- `languages` (object, required): key = language identifier (lowercase,
  `[a-z0-9_+-]+`), value:
  - `filename` (string, required): the filename the student's code is written
    to inside the workspace before linting (e.g. `solution.py`,
    `Solution.java`).
  - `command` (array of strings, required): argv **prefix** to exec inside
    the linter container. The caller appends one or more workspace-relative
    file paths as trailing arguments — there are no placeholders. No shell is
    involved; the argv is exec'd as-is. All files of one lint run go to a
    single invocation, and findings carry per-file paths.

## 2. Unified findings output

Every manifest `command` MUST write a single JSON document to **stdout**:

```json
{
  "version": 1,
  "language": "python",
  "tool": "pylint 3.3.9",
  "findings": [
    {
      "path": "solution.py",
      "line": 3,
      "column": 1,
      "severity": "warning",
      "rule": "W0611",
      "message": "Unused import os"
    }
  ]
}
```

- `version` (int, required): output-schema version, currently `1`.
- `language` (string, required): the manifest key that was invoked.
- `tool` (string, required): freeform tool name + version, for display and
  debugging.
- `findings` (array, required, may be empty):
  - `path` (string, required): path as given on the command line
    (workspace-relative).
  - `line` (int, required): 1-based line; `0` when unknown/file-scoped.
  - `column` (int, optional): 1-based column; `0`/absent when unknown.
  - `severity` (string, required): one of `error`, `warning`, `convention`,
    `refactor`, `info`. Tool-native categories are mapped by the language
    implementation (e.g. pylint `E`/`F`→`error`, `W`→`warning`,
    `C`→`convention`, `R`→`refactor`; checkstyle `error`→`error`,
    `warning`→`warning`, `info`→`info`).
  - `rule` (string, optional): tool-native rule/check id (e.g. `W0611`,
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
  Python syntax error is an ordinary pylint `E0001` finding (exit `0`).

## 4. Invocation environment

- Working directory: `/workspace` (core copies the student files there under
  the manifest `filename` before invoking the command).
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

Implement the `linter.Linter` interface (`Language`, `Filename`, `Command`,
`Parse`) in a new `languages/<lang>/` package and register it in
`languages/languages.go` — the manifest, the CLI, and the conformance tests
all derive from that registry automatically.
