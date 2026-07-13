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
| Pylint (Python 3) | `python313.PylintVersion` (3.3.*) | `pylint --output-format=json --disable=C0114,C0115,C0116 <files>` |
| Checkstyle | `java.CheckstyleVersion` (10.21.1, JRE from Debian) | `java -jar /opt/checkstyle.jar -c /opt/checkstyle-config.xml -f xml <files>` |
| Clang-Tidy (c, cpp11, cpp14) | Debian 13 (trixie) repositories (LLVM 19) | `clang-tidy <files> -- -std=<pinned standard>` |
| Go | `golang.GoVersion` (1.24.0) | `go vet <files>` |

Each pin lives as an exported const in its `languages/<lang>` package — a
one-line diff to bump — and the Dockerfile build stage bakes them into the
install steps via `cmd/toolversions`.

## Language versions

What language level each linter checks, pinned or probed in the image so a
toolchain bump cannot silently move it:

| Language | Linted as | Determined by |
|---|---|---|
| python313 | Python 3.13.5 | the image's `python3` interpreter running pylint (Debian 13) |
| java | Java syntax up to 21 | `java.CheckstyleVersion` grammar, on OpenJDK 21 (`default-jre-headless`) |
| c | `-std=gnu17` | `c.CStandard` (pins clang 19's probed default) |
| cpp11 | `-std=gnu++11` | `cpp11.CppStandard` (GNU dialect, matching the gnu17 precedent) |
| cpp14 | `-std=gnu++14` | `cpp14.CppStandard` (GNU dialect, matching the gnu17 precedent) |
| go | Go 1.24 | `golang.GoVersion` toolchain's typechecker |

## Key paths

- `/usr/local/bin/cobe-lint` — the unified CLI (the only path core hardcodes)
- `/opt/checkstyle.jar`, `/opt/checkstyle-config.xml` — Checkstyle JAR and
  configuration (based on Google's Java Style Guide)
- `/workspace` — working directory for lint runs

The image runs as the non-root `linter` user.

## Adding a language (fork guide)

In short: implement `linter.Linter` in `languages/<lang>/` (with an inline-
fixture `Parse` unit test and a version const), install the tool in the
[`Dockerfile`](Dockerfile), and register the language in
[`languages/languages.go`](languages/languages.go) — everything else
(manifest, CLI, conformance tests) derives from the registry. The workspace
filename the language lints under is core's deployment config, not this
repo's. See [`docs/ADDING_A_LANGUAGE.md`](docs/ADDING_A_LANGUAGE.md) for a
complete worked example (shellcheck).

## Tags

| Tag | Meaning |
|---|---|
| `latest` | most recent build of `main` (also refreshed by the monthly scheduled rebuild) |
| `X.Y.Z`, `X.Y`, `X` | pushed on git tags `vX.Y.Z` — pin these in production config |
| `sha-<commit>` | every non-PR build, for traceability |

CI (`.github/workflows/publish.yml`) lints the Dockerfile, runs the unit and
conformance tests against a freshly built image, and publishes multi-arch
(amd64/arm64) images on pushes, tags, and a monthly no-cache rebuild.

## Local build & test

```bash
go vet ./... && go test ./...     # unit tests (parsers, CLI, registry)
docker build -t cobe-linter:dev .
go test -tags conformance ./...   # image conformance (IMAGE=<tag> to override)
```
