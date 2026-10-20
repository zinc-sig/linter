# cobe-linter

Multi-language linter image for COBE's sandboxed lint runs.

```bash
docker pull ghcr.io/zinc-sig/linter:latest
```

## Unified interface

The image ships one static CLI, `/usr/local/bin/cobe-lint`, which implements
the whole contract with [zinc-sig/core](https://github.com/zinc-sig/core):
`cobe-lint manifest` prints the language manifest (language ids, display
names, and lint commands), and `cobe-lint lint <language> <file> [<file>...]` runs the native
tool and prints unified findings JSON — exit `0` means findings are data,
non-zero means operational failure. Workspace filenames are core's
deployment config, not the image's. [`docs/CONTRACT.md`](docs/CONTRACT.md)
is the authoritative spec.

## Image contents

| Toolchain | Version | Native invocation (run by `cobe-lint`) |
|---|---|---|
| Ruff (python312, python313) | 0.15.21 (one native binary shared by the python languages, installed by a pinned [uv](https://github.com/astral-sh/uv)) | `/usr/local/bin/ruff check --no-cache --output-format=json --target-version py<NN> <files>` |
| Checkstyle | 10.21.1 (on a jlink'ed minimal Java 21 runtime) | `/opt/java/bin/java -jar /opt/checkstyle.jar -c /opt/checkstyle-config.xml -f xml <files>` |
| Clang-Tidy (c, cpp11, cpp14) | Debian 13 (trixie) repositories (LLVM 19) | `clang-tidy <files> -- -std=<pinned standard>` |
| Go | 1.24.0 | `go vet <files>` |

Each pin lives in the `tools:` section of
[`languages/manifest.yaml`](languages/manifest.yaml) — a one-line diff to
bump — and the Dockerfile build stage bakes them into the install steps
via `cmd/toolversions`.

## Language versions

What language level each linter checks, pinned or probed in the image so a
toolchain bump cannot silently move it:

Every value below is a field of the language's stanza in
[`languages/manifest.yaml`](languages/manifest.yaml):

| Language | Linted as | Determined by |
|---|---|---|
| python312 | Python 3.12 | `with.target: py312` (ruff `--target-version`) |
| python313 | Python 3.13 | `with.target: py313` (ruff `--target-version`) |
| java | Java syntax up to 21 | the pinned checkstyle grammar, on a jlink'ed OpenJDK 21 runtime |
| c | `-std=gnu17` | `with.std: gnu17` (pins clang 19's probed default) |
| cpp11 | `-std=gnu++11` | `with.std: gnu++11` (GNU dialect, matching the gnu17 precedent) |
| cpp14 | `-std=gnu++14` | `with.std: gnu++14` (GNU dialect, matching the gnu17 precedent) |
| go | Go 1.24 | the pinned Go toolchain's typechecker |

Python versions stay decoupled from the base image, and more simply than
before: no interpreter ships at all. One pinned ruff binary lints every
Python line, so adding e.g. python314 is a new stanza whose `with:` block
sets `target: py314` — no interpreter or virtualenv install behind it.

## Key paths

- `/usr/local/bin/cobe-lint` — the unified CLI (the only path core hardcodes)
- `/usr/local/bin/ruff` — the shared ruff binary behind the python languages
- `/opt/checkstyle.jar`, `/opt/checkstyle-config.xml` — Checkstyle JAR and
  configuration (based on Google's Java Style Guide)
- `/opt/java` — jlink'ed minimal Java runtime that runs checkstyle
- `/workspace` — working directory for lint runs

The image runs as the non-root `linter` user.

## Adding a language (fork guide)

In short: add a stanza to
[`languages/manifest.yaml`](languages/manifest.yaml) — everything else
(manifest JSON, CLI, conformance tests) derives from that file. When the
language needs a tool the image doesn't have yet, also implement the
`linter.Linter` driver in `languages/internal/<tool>/` (with an
inline-fixture `Parse` unit test), install the tool in the
[`Dockerfile`](Dockerfile), and pin its release in the manifest's `tools:`
section. The workspace filename the language lints under is core's
deployment config, not this repo's. See
[`docs/ADDING_A_LANGUAGE.md`](docs/ADDING_A_LANGUAGE.md) for a complete
worked example (shellcheck).

## Tags

| Tag | Meaning |
|---|---|
| `latest` | most recent build of `main` |
| `X.Y.Z` | minted automatically on every push to `main` (patch bump of the latest `vX.Y.Z` git tag) — pin these in production config |
| `X.Y`, `X` | published alongside manually pushed git tags `vX.Y.Z` |
| `sha-<commit>` | every non-PR build, for traceability |

CI (`.github/workflows/publish.yml`) lints the Dockerfile, runs the unit and
conformance tests against a freshly built image, and publishes multi-arch
(amd64/arm64) images on pushes and tags. Every `main` push also creates the
next `vX.Y.Z` git tag itself, so versions need no manual tagging and are never
re-published.

## Local build & test

```bash
go vet ./... && go test ./...     # unit tests (parsers, CLI, manifest)
docker build -t cobe-linter:dev .
go test -tags conformance ./...   # image conformance (IMAGE=<tag> to override)
```
