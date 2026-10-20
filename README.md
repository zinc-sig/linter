# cobe-linter

Multi-language linter image for COBE's sandboxed lint runs.

```bash
docker pull ghcr.io/zinc-sig/linter:latest
```

## Unified interface

The image ships one static CLI, `/usr/local/bin/cobe-lint`, which implements
the whole contract with [zinc-sig/core](https://github.com/zinc-sig/core).
[`docs/CONTRACT.md`](docs/CONTRACT.md) is the authoritative spec.

- `cobe-lint serve` is the container's main process (the image's default
  command, under `tini`). It is an HTTP/1.1 JSON server on the unix socket
  `/tmp/cobe-lint.sock`, plus an optional TCP listener that requires a
  shared token (`--listen :8080 --token-file <path>`, used on Kubernetes).
  Core sends `POST /v1/lint` with the language, filename and code and gets
  the unified findings report back. The server lints with warm tools: a
  resident checkstyle JVM for Java, a Go build cache warmed at startup,
  and ruff or clang-tidy as one-shot processes in a fresh workspace per
  request. It bounds concurrency with `--workers` and a short queue,
  answers `busy` beyond that, enforces a per-request deadline (`8s`), keeps
  a per-question asset cache (`PUT /v1/assets/...`), and reports
  `/healthz/live` and `/healthz/ready`.
- `cobe-lint dial-stdio` bridges its stdin and stdout to that socket. On
  Docker, core opens each pooled connection as an exec of it, so a lint
  costs no exec.
- `cobe-lint manifest` prints the language manifest (language ids, display
  names, and lint commands), and `cobe-lint lint <language> <file>
  [<file>...]` runs the native tool once and prints unified findings JSON:
  exit `0` means findings are data, non-zero means operational failure.
  Both stay for debugging and the conformance suite.

Workspace filenames are core's deployment config, not the image's.

```bash
# Run the server the way core does on Docker and lint through dial-stdio.
docker run -d --name linter --network none cobe-linter:dev \
  /usr/local/bin/cobe-lint serve --socket /tmp/cobe-lint.sock --workers 4
printf 'GET /healthz/ready HTTP/1.1\r\nHost: linter\r\nConnection: close\r\n\r\n' \
  | docker exec -i linter /usr/local/bin/cobe-lint dial-stdio
```

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
- `/opt/checkstyle-worker.jar`, `/opt/checkstyle-warmup/Warmup.java` — the
  resident checkstyle worker `cobe-lint serve` keeps running for Java
  lints, and its warm-up sample (source and Go supervisor in
  `internal/checkstyleworker`; protocol in docs/CONTRACT.md Appendix A)
- `/usr/bin/tini` — the entrypoint; it runs as PID 1 and reaps the
  orphaned children of killed tools
- `/tmp/cobe-lint.sock` — the server's unix socket (`--socket`)
- `/tmp/cobe-lint-ws-*` — per-request lint workspaces (`--work-dir`),
  removed after each lint
- `/workspace/lint-cache` — the per-question asset cache (`--cache-dir`),
  emptied at every start
- `/tmp/cobe-gocache` — the Go build cache `go vet` uses, warmed once at
  startup

Source: the server in `internal/serve`, `dial-stdio` in
`internal/dialstdio`, the checkstyle worker in `internal/checkstyleworker`.
The image runs as the non-root `linter` user, and its label
`org.zinc-sig.linter.serve-protocol` names the serve protocol it
implements.

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
go vet ./... && go test ./...     # unit tests (parsers, CLI, manifest, serve, dial-stdio, worker supervisor)
docker build -t cobe-linter:dev .
go test -tags conformance ./...   # image conformance (IMAGE=<tag> to override)
# Java throughput through dial-stdio against a core-shaped container
COBE_LINT_BENCH=1 go test -tags conformance -run TestServeJavaThroughput -v ./tests/
```
