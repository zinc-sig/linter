# cobe-linter

Multi-language linter image for COBE's sandboxed lint runs.

```bash
docker pull ghcr.io/zinc-sig/linter:latest
```

## Unified interface

The image ships one static CLI, `/usr/local/bin/cobe-lint`, which implements
the whole contract with [zinc-sig/core](https://github.com/zinc-sig/core):
`cobe-lint manifest` prints the language manifest (filenames and lint
commands), and `cobe-lint lint <language> <file> [<file>...]` runs the native
tool and prints unified findings JSON — exit `0` means findings are data,
non-zero means operational failure. [`docs/CONTRACT.md`](docs/CONTRACT.md)
is the authoritative spec.

## Image contents

| Toolchain | Version | Native invocation (run by `cobe-lint`) |
|---|---|---|
| Pylint (Python 3) | pinned via `PYLINT_VERSION` build arg | `pylint --output-format=json --disable=C0114,C0115,C0116 <files>` |
| Checkstyle | pinned via `CHECKSTYLE_VERSION` build arg (JRE from Debian) | `java -jar /opt/checkstyle.jar -c /opt/checkstyle-config.xml -f xml <files>` |
| Clang-Tidy (c, cpp) | Debian 13 (trixie) repositories | `clang-tidy <files> --` |
| Go | pinned via `GO_VERSION` build arg | `go vet <files>` |

Current pins live at the top of the [`Dockerfile`](Dockerfile) as `ARG`s —
bumping a linter version is a one-line diff.

## Key paths

- `/usr/local/bin/cobe-lint` — the unified CLI (the only path core hardcodes)
- `/opt/checkstyle.jar`, `/opt/checkstyle-config.xml` — Checkstyle JAR and
  configuration (based on Google's Java Style Guide)
- `/workspace` — working directory for lint runs

The image runs as the non-root `linter` user.

## Adding a language (fork guide)

1. Install the tool in the [`Dockerfile`](Dockerfile) (pin the version via an
   `ARG`).
2. Implement the `linter.Linter` interface in `languages/<lang>/`, with a
   `Parse` unit test on captured tool output in `languages/<lang>/testdata/`.
3. Register it in [`languages/languages.go`](languages/languages.go) and add
   `tests/testdata/<lang>/{clean,dirty}/<filename>` samples.

Then run `go test ./...` and the conformance suite (below) — both discover
languages from the registry/manifest automatically.

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
