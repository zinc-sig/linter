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

Core runs `cobe-lint serve` as the linter container's main process and
talks to it over HTTP (sections 7 to 11). Sections 1 to 4 define the
documents and the one-shot CLI (`cobe-lint manifest`, `cobe-lint lint`)
that the server is built on; the CLI stays available for debugging and
the conformance suite. Appendix A describes an image-internal protocol
that core never sees.

## 1. Language manifest — `cobe-lint manifest`

`/usr/local/bin/cobe-lint manifest` writes the manifest JSON to stdout.
Core reads the same document, plus a `serve` object, from
`GET /v1/manifest` (section 8.3) at startup and derives the
supported-language set from it (also served via core's public "supported
languages" endpoint). The `command` field below describes the one-shot CLI;
core no longer execs it, but it stays required for compatibility.

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
  (e.g. `ruff`, `javac`, `clang-tidy`, `go vet`) that consumers may
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
    `W`→`warning`, pycodestyle `E`→`convention`; javac errors→`error`,
    warnings→`warning`, notes dropped).
  - `rule` (string, optional): tool-native rule/check id (e.g. `F401`,
    `compiler.err.cant.resolve.location`,
    `clang-analyzer-core.NullDereference`).
  - `message` (string, required): human-readable description.

## 3. Exit-code semantics

- **`0`** — the lint ran to completion. Findings (including zero findings) are
  in the JSON on stdout. A lint that found errors in the student's code still
  exits `0`: findings are data, not failure.
- **non-zero** — operational failure (tool crashed, unparseable output, bad
  arguments, unknown language, unreadable file). Stdout may be empty or
  partial; the CLI writes a diagnostic to stderr. Core reports this as a
  lint-execution error, not as findings. Under serve the same outcome is
  the `linter_execution_failed` error (section 8.2).
- Where the boundary sits follows the native tool. A syntax error is an
  ordinary finding for every language (ruff `invalid-syntax`, javac
  `compiler.err.*`, exit `0`), while a file the tool cannot read is a
  failure.

## 4. Invocation environment

- Working directory: a workspace directory the caller prepares per
  invocation (under serve, the server itself; section 9.2), containing the
  student file (named per the instructor's question configuration)
  alongside any sibling assets (headers, provided sources).
  File paths passed to `cobe-lint` are relative to that directory; linters may
  resolve sibling files through the working directory. The image's
  `/workspace` merely guarantees a writable default location.
- The container runs as the non-root `linter` user; commands must not require
  root.
- The caller enforces the execution timeout and output-size caps (under
  serve, the server's per-request deadline and findings cap; section 9.1);
  the CLI needs no timeout logic.

## 5. Compatibility rules

- Adding a language, or adding optional finding fields, is backward-compatible
  within version 1.
- Renaming/removing manifest fields, changing severity values, or changing
  exit-code semantics requires bumping `version` in both manifest and output.
- The serve protocol (sections 7 to 11) is versioned separately by
  `serve.protocol` and the `/v1` path prefix (section 8.3). Adding an
  endpoint, an optional request or response field, or an error code that
  clients may treat as a generic failure is backward-compatible within
  protocol 1. Anything else bumps the protocol and the path prefix.

## 6. Adding a language

Add a stanza to `languages/manifest.yaml` binding the new language key and
display name to a tool driver — when the tool is new to the image, also
implement the `linter.Linter` interface (`Language`, `Name`, `Command`,
`Parse`) in a `languages/internal/<tool>/` package. The manifest JSON, the
CLI, and the conformance tests all derive from that file automatically. No
filename is involved: the workspace filename for the new language is
configured on core's side as deployment config.

## 7. Serve mode: `cobe-lint serve`

`cobe-lint serve` is the linter container's main process: a resident
HTTP/1.1 server with a JSON API (section 8) that lints with warm tools
(section 9). The container is still one singleton container or pod that
core provisions, supervises and replaces; only the interface core uses to
reach the tools changes, from one exec per step to HTTP requests.

### 7.1 Flags

| Flag | Default | Meaning |
|---|---|---|
| `--socket PATH` | `/tmp/cobe-lint.sock` | Unix socket the server always listens on. |
| `--listen ADDR` | empty (off) | Optional TCP listener, a Go `net.Listen` address such as `:8080`. Requires `--token-file`. |
| `--token-file PATH` | empty | File holding the TCP shared token (section 7.2). Only valid with `--listen`. |
| `--workers N` | `4` | Maximum number of lints executing at once, across all languages (1 to 64). Also sizes the resident javac thread pool. Core passes its `max_concurrent`. |
| `--queue N` | value of `--workers` | Lints that may wait for a worker (0 to 256). Beyond workers plus queue the server answers `busy` at once. |
| `--deadline DUR` | `8s` | Per-request deadline of a lint, a Go duration greater than zero. Kept below core's 10 s lint budget so the server answers before core gives up. |
| `--cache-dir PATH` | `/workspace/lint-cache` | Root of the asset cache (section 9.2). |
| `--cache-max-bytes N` | `201326592` (192 MiB) | Byte budget of the asset cache; also the largest accepted asset upload. |
| `--work-dir PATH` | `/tmp` | Parent directory of the per-request workspaces. |

Flag values are fixed for the process lifetime. A flag error (unknown flag,
`--listen` without `--token-file`, `--token-file` without `--listen`, a
value out of range) is a usage error.

### 7.2 Listeners and authentication

- **Unix socket.** Always on. The server removes a stale file at the path,
  binds, and sets mode `0600`. The socket is reachable only through the
  container runtime's exec API (section 10), so requests on it need no
  token; a token header on a socket request is ignored.
- **TCP.** Only with `--listen`, used on Kubernetes. Every request except
  `GET /healthz/live` and `GET /healthz/ready` must carry the header
  `X-Zinc-Linter-Token` whose value equals the token, compared in constant
  time; a missing or different value answers `401 unauthorized`. The check
  runs before routing, so an unknown path or a wrong method without the
  token also answers `401`, not `404` or `405`. The token
  travels in this header, not in `Authorization`, because the Kubernetes
  API server consumes `Authorization` on the `pods/proxy` path. The health
  endpoints are exempt so that kubelet probes, which cannot read a Secret,
  work; they disclose no data.
- **Token file.** Read once at startup. Trailing whitespace is trimmed;
  the remaining token must be at least 32 bytes, otherwise startup fails.
  Core writes 32 random bytes, hex encoded (64 characters), per pod start.
- **Token confidentiality.** The server never logs the token, never puts
  it or the token file path into a tool's environment (section 9.3), and
  replaces every occurrence of the token in a response body with
  `[redacted]` before sending it. A tool's stdout and stderr are redacted
  as soon as the tool exits, before parsing and before any message or
  stderr snippet is truncated, so that no cap can cut the token into a
  prefix the redaction no longer matches; the encoded body is redacted
  again as a backstop. The redaction exists because the tools
  run as the same user as the server and can read the mounted token file:
  a C source that includes the file would otherwise echo it back in a
  diagnostic. The server also marks itself non-dumpable
  (`prctl(PR_SET_DUMPABLE, 0)`) so tools cannot read its memory or
  environment through `/proc`.
- **No TLS.** Both listeners speak plain HTTP. The TCP listener is meant
  for the pod network behind a NetworkPolicy and for the API server proxy.

### 7.3 Lifecycle and exit codes

1. Parse flags, read the token file, create the cache and work
   directories, bind the listeners. Any failure here exits. Everything
   inside `--cache-dir` and every `cobe-lint-ws-*` directory inside
   `--work-dir` left by an earlier process is removed, and both
   directories are checked for writability.
2. Serve at once. Until ready, `GET /v1/manifest`, the asset endpoint and
   both health endpoints work, `POST /v1/lint` answers
   `503 linter_not_ready`, and `GET /healthz/ready` answers 503 `starting`.
3. Warm up concurrently: start and warm the resident javac worker
   when any manifest language uses javac (section 9.6), and run the
   Go build-cache warm-up when any language uses `go vet` (section 9.5).
4. Become ready once the worker pool runs, the javac worker (if
   needed) answered its warm-up, and the Go warm-up (if needed) finished,
   successfully or not.
5. On SIGTERM or SIGINT: close the listeners, cancel in-flight lints,
   stop the javac worker, remove the socket file, and exit within
   2 seconds.

| Exit code | Meaning |
|---|---|
| `0` | Shut down on SIGTERM or SIGINT. |
| `1` | Startup failure: token file unreadable or too short, a listener cannot bind, the cache or work directory is not writable. A diagnostic goes to stderr. |
| `2` | Usage error (bad flag). A `cobe-lint` that predates serve also exits `2`, with `unknown command "serve"`, which is one way core recognises an old image (section 11.5). |

The server logs one JSON line per request to stderr: method, path,
status, error code, language, queue and run milliseconds, and the W3C
trace id when a `traceparent` header was sent. It never logs code, asset
contents or the token.

### 7.4 Image defaults and the argv core passes

- The image's `ENTRYPOINT` is `["/usr/bin/tini", "--"]` and its `CMD` is
  `["/usr/local/bin/cobe-lint", "serve"]`. tini runs as PID 1 and reaps
  orphaned grandchildren (for example the compiler processes of a killed
  `go vet`), which would otherwise stay zombies counted against the
  256-pid limit.
- Core keeps the entrypoint: on Docker it sets only `Cmd`, on Kubernetes
  only the container's `args`, never `command` (which would replace
  tini). On Docker the argv is

  ```
  /usr/local/bin/cobe-lint serve --socket /tmp/cobe-lint.sock --workers <max_concurrent>
  ```

  and on Kubernetes the same plus
  `--listen :8080 --token-file <mounted token path>`.
- The image carries the label `org.zinc-sig.linter.serve-protocol` set to
  the serve protocol it implements (`1`). Core may read it from the image
  before creating the container (section 11.5).
- `docker run <image> cobe-lint lint ...` and `... cobe-lint manifest`
  keep working for debugging.

## 8. Serve HTTP API (protocol 1)

### 8.1 Conventions

- HTTP/1.1 with keep-alive; no HTTP/2. The `Host` header is ignored.
  Paths match exactly, without trailing slashes; an unknown path answers
  `404 not_found` and a known path with another method answers
  `405 method_not_allowed` with an `Allow` header.
- Request `Content-Type` is not checked. Response bodies are
  `application/json; charset=utf-8`, except `204` responses, which have
  none.
- A JSON request body is one object; unknown fields are ignored; trailing
  data after the object is a `bad_request`.
- Every error response has this body:

  ```json
  {"error": {"code": "busy", "message": "All lint workers are busy.", "details": ""}}
  ```

  `code` (string, required) is from section 8.2; `message` (string,
  required) is a human-readable sentence; `details` (string, optional) is
  diagnostic text such as a tool's stderr snippet. The three fields match
  core's `LintError`.
- Limits: request headers at most 64 KiB; a `POST /v1/lint` body at most
  4 MiB; an asset upload body at most `--cache-max-bytes`. A larger body
  answers `413 payload_too_large`.
- Connection timeouts: request headers must arrive within 5 seconds of a
  request's first byte; an idle keep-alive connection is closed after 120
  seconds. The idle timeout is longer than the 90 seconds core's client
  keeps idle connections, so the client normally closes first and a reused
  connection is rarely dead.
- Headers the server accepts and ignores: `Idempotency-Key` (section
  11.3), and the W3C `traceparent` and `tracestate`, used only for log
  correlation (the server has no network to export spans).
- `POST /v1/lint` responses carry `X-Zinc-Linter-Queue-Ms` (time spent
  waiting for a worker) and `X-Zinc-Linter-Run-Ms` (time spent linting)
  as decimal integers, when the request got that far. Core may record
  them as span attributes.

### 8.2 Error codes

| Code | Status | Returned when | Core's `LintError.Code` and API outcome (section 11.4) |
|---|---|---|---|
| `bad_request` | 400 | Malformed JSON, a missing or mistyped field, a field failing its pattern. | passed through; 503 |
| `unauthorized` | 401 | TCP request without the right token. | passed through; 503 |
| `not_found` | 404 | Unknown path. | passed through; 503 |
| `method_not_allowed` | 405 | Wrong method for a path. | passed through; 503 |
| `assets_required` | 409 | The lint names a cache key and fingerprint that the cache does not hold. | handled inside core (section 11.3), never surfaced |
| `payload_too_large` | 413 | Body, `code`, or asset set over its limit. | passed through; 503 |
| `filename_invalid` | 422 | `filename` fails the rules of section 8.4. | `filename_invalid`; 400 |
| `unsupported_language` | 422 | `language` is not a manifest key. | `unsupported_language`; 400 |
| `assets_invalid` | 422 | The upload is not an acceptable tar (section 9.2). | upload failure; the lint proceeds without assets |
| `linter_execution_failed` | 500 | The tool failed operationally (the non-zero case of section 3, unparseable output, a javac worker error reply). `details` carries the diagnostic the CLI would write to stderr. | `linter_execution_failed`; 503 |
| `workspace_setup_failed` | 500 | The per-request workspace could not be created or populated (for example ENOSPC). | `workspace_setup_failed`; 503 |
| `internal` | 500 | Any other server fault. | passed through; 503 |
| `busy` | 503 | Workers and queue are full, or too many asset uploads are in progress. Carries `Retry-After: 1`. | `busy`; 503 |
| `linter_not_ready` | 503 | Not ready yet (section 7.3), the javac worker restarted under the request, or the javac worker failed for good. Carries `Retry-After: 1`. | `linter_not_ready`; 503 "Lint service unavailable" |
| `timeout` | 504 | The deadline passed while the request was queued, waiting for the javac worker, or running. | `timeout`; 503 |
| `cache_full` | 507 | The asset cache cannot make room for an upload. | upload failure; the lint proceeds without assets |

The codes core produces without asking the server stay core-side:
`empty_code`, `filename_not_configured`, `linter_output_invalid` (a `200`
body that does not parse as a section 2 report), and
`container_exec_failed` for transport failures (section 11.4).

### 8.3 `GET /v1/manifest`

Answers `200` from startup on, ready or not, with the section 1 manifest
plus a `serve` object:

```json
{
  "version": 1,
  "languages": {
    "python313": {
      "name": "Python 3.13",
      "command": ["/usr/local/bin/cobe-lint", "lint", "python313"]
    }
  },
  "serve": {
    "protocol": 1,
    "workers": 4,
    "queue": 4,
    "deadline_ms": 8000,
    "max_code_bytes": 1048576,
    "max_findings": 1000,
    "cache_max_bytes": 201326592
  }
}
```

- `version` and `languages` are exactly what `cobe-lint manifest` prints.
  `cobe-lint manifest` itself does not print `serve`.
- `serve.protocol` (int, required): the serve protocol version, `1` for
  this document; it matches the `/v1` path prefix. A client rejects a
  manifest without `serve` or with a protocol it does not implement.
- The other `serve` fields (int, required in protocol 1) report the
  effective flag values and limits: `workers`, `queue`, `deadline_ms`
  (`--deadline` in milliseconds), `max_code_bytes` (the largest `code`,
  section 8.4), `max_findings` (the largest accepted `max_findings`),
  and `cache_max_bytes`. Clients size their own limits from them (section
  11.2) and must not reject a manifest because of their values.

### 8.4 `POST /v1/lint`

Request:

```json
{
  "language": "java",
  "filename": "src/app/Solution.java",
  "code": "package app;\n...",
  "assets": {
    "cache_key": "0f8c1d2e-6a4b-4c3d-9e8f-1a2b3c4d5e6f",
    "fingerprint": "9b74c9897bac770ffc029102a200c5de3f1f3c1e2a8b7d6c5e4f3a2b1c0d9e8f"
  },
  "max_findings": 100
}
```

- `language` (string, required): a manifest key, else
  `422 unsupported_language`.
- `filename` (string, required): the workspace-relative path the code is
  written to and linted under. It must satisfy every rule below, the same
  rules as core's `pipeline.ValidateSubmissionPath`, else the request
  answers `422 filename_invalid`:
  - non-empty and at most 255 bytes;
  - no control character (below 0x20, or 0x7f) and no backslash;
  - does not start with `/` or `-`;
  - not `.`, and unchanged by Go's `path.Clean` (no `./`, `//`, or
    trailing slash);
  - no `..` segment;
  - the first segment is neither `.zinc` nor `examination-assets`.
- `code` (string, required): the file content, written as its UTF-8
  bytes. It may be empty; the whitespace-only check is core's. More than
  1,048,576 bytes answers `413 payload_too_large`.
- `assets` (object, optional): selects the asset set copied next to the
  file (section 9.2). When present, both fields are required:
  - `cache_key` (string): matches `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`;
  - `fingerprint` (string): matches `^[0-9a-f]{64}$`.

  A pattern mismatch is `400 bad_request`. When the cache holds no entry
  for `cache_key` with exactly this `fingerprint`, the request answers
  `409 assets_required` without linting. When `assets` is absent, the
  lint runs without assets.
- `max_findings` (int, optional): keep at most this many findings, the
  first ones in the order the tool reported them. Default and maximum
  1000; a value below 1 or above 1000 is a `bad_request`.

Field validation runs in the order listed here, before readiness and
admission (section 9.1), so a bad request gets its own error even while
the server is busy or starting. The cache lookup behind `assets_required`
happens after admission, when the workspace is prepared; from that lookup
until the copy finishes, the entry counts as being read and is not
evicted.

Success is `200` with the section 2 report: the document
`cobe-lint lint <language> <filename>` prints when it exits `0`, after
the `max_findings` cap and with each `message` truncated to 4096 bytes.
Findings, possibly none, are data. A finding on the student file has
`path` equal to `filename`. A `200` body is at most 8 MiB.

### 8.5 `PUT /v1/assets/{cache_key}/{fingerprint}`

Stores the asset set of one cache key, replacing whatever the key held.
`cache_key` and `fingerprint` follow the patterns of section 8.4; a
mismatch is `400 bad_request`. The body is an uncompressed tar stream
whose accepted entries section 9.2 lists; an empty archive stores an
empty set.

| Status | Meaning |
|---|---|
| `204` | Stored. Also the answer, with nothing changed, when the key already holds this fingerprint. |
| `400 bad_request` | Bad path parameters, or a body that is not a tar stream. |
| `413 payload_too_large` | Body or extracted size over `--cache-max-bytes`. |
| `422 assets_invalid` | An entry type, name, duplicate, or entry count that section 9.2 rejects. |
| `503 busy` | `--workers` uploads are already in progress. |
| `504 timeout` | The upload did not finish within 60 seconds. |
| `507 cache_full` | No room can be made (the other entries are in use, or ENOSPC). |

Uploads take no lint worker slot. PUT is idempotent, so clients may retry
it on a dead connection.

### 8.6 `GET /healthz/live`

A liveness heartbeat answered by the server's own goroutine; it never
waits for a worker, the queue, or the javac worker.

- `200 {"status": "ok"}` while the server is responsive.
- `503 {"status": "stuck", "reason": "..."}` when a worker slot has been
  held by one request, or a request has been on the javac worker,
  for more than three times `--deadline` (24 seconds by default). The
  second case covers Java requests already answered with `504 timeout`,
  which leave their worker slot but keep their javac thread. One-shot
  tools are killed at the deadline and the javac worker is restarted
  once a request has run on it for `--deadline` (sections 9.4 and 9.6), so
  only a process the kernel cannot kill reaches this state.

### 8.7 `GET /healthz/ready`

- `503 {"status": "starting", "pending": ["javac", "go-warmup"]}`
  before startup step 4 (section 7.3); `pending` lists the components
  still warming up, from `workers`, `javac` and `go-warmup`.
- `200 {"status": "ready"}` from startup step 4 on. Ready stays `200`
  through routine recovery, such as a javac worker restart, during
  which Java lints wait or time out (section 9.6).
- `503 {"status": "failed", "reason": "..."}` on a terminal failure: the
  javac worker failed to start three times in a row. The server does
  not recover from it; replacing the container is the remedy.

Ready is therefore monotone: `starting` only before the first `200`, and
a 503 after the first `200` means the container must be replaced
(section 11.2).

## 9. Lint execution under serve

### 9.1 Admission, deadline and findings cap

- A lint request is validated (sections 8.1 and 8.4), then checked for
  readiness, then takes a worker slot. When `--workers` lints run and
  `--queue` more wait, it answers `503 busy` at once instead of waiting.
  Health and manifest requests never take a slot; uploads have their own
  limit (section 8.5).
- The deadline starts when the request's headers have been read and
  covers reading the body, queueing, workspace setup, asset copying, the
  tool run and parsing. When it passes, the request answers
  `504 timeout`.
- A one-shot tool's stdout and stderr are each capped at 16 MiB; a tool
  that writes more is a `linter_execution_failed`.
- Findings are truncated to `max_findings`, and each message to 4096
  bytes without splitting a UTF-8 sequence, after the report is built.

### 9.2 Workspace and asset cache

**Workspace.** Each lint gets a fresh directory
`<work-dir>/cobe-lint-ws-<random>` with mode `0700`. The server copies the
selected asset set in first, then writes `code` to `filename`, creating
parent directories, so the student file replaces an asset of the same
path. An asset file where `filename` needs a directory is a
`workspace_setup_failed`. The tool runs with the workspace as its working
directory and `filename` as its file argument (section 4). The directory
is removed after the tool exits; for a Java request, when the request is
answered or abandoned. A Java request that passed its deadline may still
be running on the javac worker and then finds its file gone; its
reply is discarded either way.

**Cache layout.** Entries live under `--cache-dir`, one per cache key, and
each holds one fingerprint. Entries contain only regular files and
directories. Copying an entry into a workspace uses `lstat` and copies
only regular files and directories, so nothing outside the cache is ever
followed.

**Upload body.** A POSIX tar stream (ustar or PAX, as Go's `archive/tar`
writes). The accepted entry types are regular files (`0` and the legacy
NUL type) and directories (`5`); PAX extended headers apply to their
entry, and PAX global headers are ignored. Any other entry type,
including symbolic links, hard links, devices and FIFOs, rejects the whole
upload with `assets_invalid`. Entry names:

- a leading `./` is removed, and so is a trailing `/` on a directory;
- the result is non-empty, at most 4096 bytes, has no NUL or other control
  character, does not start with `/`, is unchanged by `path.Clean`, and
  has no `..` segment;
- no name appears twice, and no file name is also used as a directory.

Asset names are not checked against the reserved first segments of
section 8.4, because assets may live under `examination-assets/`. Mode
bits, owners and times in the archive are ignored; files are written
`0644` and directories `0755`. At most 10,000 entries.

**Atomic replace.** An upload extracts into a staging directory on the
same filesystem and renames it into place only when complete; a failed
upload never alters the previous entry, though it may have evicted it
(below). Uploads for one key are
serialised, and the one that completes last wins. A lint that started
copying an entry keeps reading that entry even when an upload replaces
it, and the old directory is deleted when its last reader is done. A lint
therefore sees one complete asset set, never a mix.

**Budget and eviction.** `--cache-max-bytes` bounds every file byte under
`--cache-dir`: current entries, replaced or evicted entries a lint still
reads, and the staging directories of uploads in progress. An upload
reserves each file's declared size before writing it. When the budget is
short, the server first evicts the least recently used entries no lint is
reading, the uploaded key's own entry last; when that cannot make room,
the upload answers `cache_full`. A filesystem of `--cache-max-bytes`
therefore never fills up through the cache. The server may also evict any
entry no lint is reading at any other time. The cache starts empty on
every container start. Clients treat `assets_required` as a normal cache
miss.

### 9.3 Tool environment

Every tool subprocess the server starts, including the javac worker,
gets an environment built from scratch, never the server's own:

```
PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
HOME=/tmp
TMPDIR=/tmp
LANG=C.UTF-8
```

plus the driver's `linter.Enver` defaults (for `go vet`: `GOCACHE`,
`GOPATH`, `GOTOOLCHAIN`, `GOPROXY`, `GOMAXPROCS`). No inherited variable
reaches a tool, so neither the token, the token file path, nor anything
the container runtime injected (such as Kubernetes service variables) can
be included and echoed back by a student's source through the tool's own
`/proc/self/environ`. Stdin is `/dev/null`. The version probes behind a
report's `tool` string (for example `ruff --version`) get the same
environment and run once per process.
The one-shot CLI (`cobe-lint lint`) keeps inheriting its caller's
environment, because whoever runs it by hand owns that environment.

The scrubbing covers the tools' own environments only. `tini`, PID 1,
runs as the same user and keeps the container's environment, readable at
`/proc/1/environ`, so the container must carry no secret in its
environment. Core therefore passes the token as a file, sets no
environment variables, and on Kubernetes must set
`enableServiceLinks: false` on the pod so that no service variables are
injected either.

### 9.4 Cancellation

- The request context is cancelled when the client disconnects or the
  deadline passes.
- One-shot tools (ruff, clang-tidy, `go vet`) run in their own process
  group; on cancellation the whole group gets SIGKILL, and the worker slot
  is freed once the tool has exited.
- A Java request in flight is abandoned when its client disconnects: its
  result is discarded on arrival, and its worker slot stays held until the
  javac worker replies or the request's deadline passes, so abandoned
  requests still count against `--workers`.
- A Java request whose deadline passes while it runs answers
  `504 timeout` at once, but keeps its javac thread until the
  javac worker replies, so the thread pool is never oversubscribed.
  When the request has run for `--deadline` since it was sent to the
  worker, the worker is considered poisoned, because Java cannot stop a
  thread, and is restarted (section 9.6). Time spent queued or waiting
  for a restart does not count towards poisoning.
- A request whose client is gone gets no response. A request that hit the
  deadline answers `504 timeout`.

### 9.5 Go build-cache warm-up

When any manifest language uses `go vet`, the server lints a bundled
sample as `warmup.go` once at startup, outside the worker pool, with a 2
minute timeout. The sample is a `package main` that imports the standard
packages student programs use most (`bufio`, `fmt`, `math`, `os`, `sort`,
`strconv`, `strings`). `GOCACHE` starts empty on every container start
and a cold `go vet` takes 10 seconds or more, so this single cold run
fills the cache before any request. Readiness waits for it, so no Go lint
runs cold or concurrently with it. Measured on two CPUs: the warm-up
takes about 13 seconds, and the first Go lint importing `fmt` after it
runs in about 130 ms instead of about 950 ms with an empty `main`. A
failed warm-up is logged and does not block readiness.

### 9.6 Resident javac worker

Java lints do not start a JVM each. When any manifest language uses
javac, the server starts one resident JVM as a child process (Appendix
A) with a fixed thread pool of `--workers` threads. Each request compiles
the student file through flow analysis, without writing classes, as
`javac --release <N>` would for the stanza's `with.release`.

- **Sources.** Every other regular `.java` file in the workspace (section
  9.2), outside the top-level `.zinc/` and `examination-assets/`
  directories, is compiled with the student file, so a class the
  question's assets provide resolves as it does when the submission is
  graded. Findings are reported for the student file only. Nothing is
  looked up on a source or class path. More than 500 such files fail the
  request.
- **Warm-up.** Before the server is ready, the worker checks a bundled
  sample once on every pool thread, then answers the server's warm-up
  request; that reply is the worker's readiness signal.
- **Restart.** The server restarts the worker when it exits, when it
  writes a line that is not a valid reply, or when a request is still
  unanswered `--deadline` after it was sent to the worker (poisoned). The
  poisoned request has already answered `504 timeout` at its own
  deadline; every other request in flight on the old worker answers
  `503 linter_not_ready`. Java requests that arrive during a restart hold
  their worker slot and wait for the replacement until their deadline,
  then answer `504 timeout`.
- **Terminal failure.** A start fails when the JVM does not answer the
  warm-up with a report within 60 seconds of launch. A restart after a
  warmed-up worker ends is immediate; after a failed start the next one
  waits 1 second, then 2 seconds. The third consecutive failed start
  marks the worker failed: Java lints answer `503 linter_not_ready` and
  `/healthz/ready` answers `failed`.
- **Budget.** The JVM runs with `-XX:+UseSerialGC` and a heap of 64 MB
  plus 16 MB per pool thread (`-Xmx128m` at the default four). Each pool
  thread keeps its own compiler, about 8 MB of live heap. Measured on two
  CPUs with four threads: about 145 MB resident and 17 JVM threads idle
  after the warm-up, and about 220 MB resident under load. The warm-up
  takes about 2 seconds with four threads and 11 seconds with 64. Every
  JVM thread counts against the container's pid limit.

## 10. `cobe-lint dial-stdio`

```
cobe-lint dial-stdio [--socket PATH]
```

Connects to the server's unix socket (default `/tmp/cobe-lint.sock`) and
copies stdin to the socket and the socket to stdout, so that an exec of
it gives the exec's caller a byte stream to the server, the same idea as
`docker system dial-stdio`. Core reaches the server this way on Docker.

- Both directions are copied concurrently, with no framing and no output
  of its own on stdout.
- **Half-close.** When stdin reaches EOF, it half-closes the socket's
  write side and keeps copying the socket to stdout. When the socket
  reaches EOF (the server closed the connection), it exits without waiting
  for stdin.
- It sets `GOMAXPROCS` to 1 for itself: on Docker each open connection is
  one such process inside the container's pid limit.

| Exit code | Meaning |
|---|---|
| `0` | The socket reached EOF. |
| `1` | Could not connect (socket missing or refused). Nothing was read from stdin or written to stdout; the diagnostic `cobe-lint: dial-stdio: <error>` goes to stderr. |
| `2` | Usage error. |
| `3` | I/O error after connecting; a diagnostic goes to stderr. |

## 11. Client obligations (core)

### 11.1 Reaching the server

Core uses one `net/http` client for the lint API; only the way it opens a
connection depends on the runtime.

- **Docker.** The container runs the argv of section 7.4 with
  `NetworkMode` `none`. Each new connection is an exec of
  `/usr/local/bin/cobe-lint dial-stdio` with stdin attached and `Tty`
  false. Writes go to the hijacked connection; reads come from the exec's
  stdout frames, demultiplexed with `stdcopy` (stderr frames are logged);
  `Close` closes the hijacked connection and `CloseWrite` half-closes its
  write side. The URL host is arbitrary, for example `http://linter`.
- **Kubernetes, direct.** The pod runs the argv of section 7.4 with
  `--listen :8080 --token-file <path>` and exposes container port 8080.
  Core creates a fresh random token per pod start in a Secret in the
  linter namespace, mounts it read-only, and deletes the Secret with the
  pod. Core dials `http://<pod IP>:8080`. This is the mode when core runs
  in-cluster.
- **Kubernetes, proxy.** Out of cluster (development and CI), core sends
  the same requests through the API server at
  `<api server>/api/v1/namespaces/<namespace>/pods/<pod>:8080/proxy/<path>`,
  for example `.../proxy/v1/lint`, with client-go's authenticated
  transport. The `X-Zinc-Linter-Token` header passes through unchanged.
  This traffic reaches the pod from the API server, which the
  NetworkPolicy has to admit.
- Every TCP request carries `X-Zinc-Linter-Token`; health requests may
  omit it. Core may add a kubelet `readinessProbe` (`httpGet`
  `/healthz/ready` on port 8080) so the pod reports Ready only when the
  server is ready; kubelet probes originate on the node.

### 11.2 Connections, admission and health

- Pool idle connections (`MaxIdleConnsPerHost` about `max_concurrent`,
  `IdleConnTimeout` 90 seconds), so a Docker exec is paid per connection,
  not per lint.
- Do not queue in front of the server. Core holds at most
  `serve.workers + serve.queue` lint requests in flight, read from the
  manifest at startup, taking a slot without waiting and answering `busy`
  itself when none is free. This also bounds the number of `dial-stdio`
  processes on Docker.
- Health probes use a transport separate from lint traffic and take no
  lint slot, so a saturated server is not mistaken for a dead one.
- At startup, core polls `GET /v1/manifest` until it answers, checks
  `serve.protocol` (section 11.5), then polls `GET /healthz/ready` until
  `200`. A `failed` answer during startup ends the start attempt at once
  instead of polling on. After that, the supervisor treats the container as unhealthy
  when `/healthz/live` fails or does not answer, or when `/healthz/ready`
  answers 503 (section 8.7), and replaces it.
- Propagate the W3C trace context on every request.

### 11.3 Requests, retries and the asset flow

- Every `POST /v1/lint` carries an `Idempotency-Key` header (a fresh
  random value per request) and a rewindable body (`GetBody` set, for
  example by building the request from a `bytes.Reader`). Go's
  `net/http` resends a request whose reused connection died before any
  response only when the request is idempotent, and the header marks the
  lint as safe to resend. The server ignores the header and does not
  deduplicate, because a lint has no side effect.
- The asset flow of one lint:
  1. Without assets (empty fingerprint), send the lint without `assets`.
  2. Otherwise validate `cache_key` (the question id) and `fingerprint`
     against the patterns of section 8.4; when either does not match, log
     it and send the lint without `assets`. Else send it with `assets`.
  3. On `409 assets_required`, fetch the bytes with the existing refetch
     callback, `PUT` them as a tar stream (section 8.5), and on `204`
     resend the lint once.
  4. When the refetch or the upload fails, or the resend answers
     `assets_required` again, log a warning and send the lint once more
     without `assets`. Linting without assets on cache trouble is core's
     behaviour before serve; cache trouble never fails a lint.
- Core serialises refetch and upload per cache key, as it does today, so
  concurrent lints of one question upload once.

### 11.4 Error mapping

`RunLint` keeps its signature and `LintResult` shape, and
`internal/api/sandbox/lint.go` keeps its mapping:

- `200`: parse the body with `lintcontract.ParseReport`; a parse failure
  is `linter_output_invalid`. Core reads at most 8 MiB of a response body
  and keeps its own 100-issue truncation (it may send
  `max_findings: 100`).
- An error body: copy `code`, `message` and `details` into `LintError`.
  `lint.go` then maps `filename_invalid` and `unsupported_language` to
  400, `linter_not_ready` to 503 "Lint service unavailable", and every
  other code to 503 with the message, as before.
- No usable response (dial failure, a connection reset the transport did
  not recover, an unparseable error body): `container_exec_failed`, the
  code an exec failure had before.
- Core's own context expiring first: `timeout`.

### 11.5 Minimum image

Serve protocol 1 needs `ghcr.io/zinc-sig/linter:0.0.7` or later. Core
fails startup with a message naming that minimum image on any of these
signs of an older image:

- the image label `org.zinc-sig.linter.serve-protocol` is missing or below
  `1` (Docker, where core can inspect the image);
- the linter container exits with status `2` before the server answers
  (a `cobe-lint` without serve rejects the `serve` command);
- `GET /v1/manifest` answers without a `serve` object or with a protocol
  core does not implement.

## Appendix A. javac worker protocol (image-internal)

This protocol is private to the image: core never sees it, and it may
change between image releases without a contract version bump. It is
specified here so that the server and the worker can be built separately.

**Files.** The worker is a small Java program compiled at image build, in
the `eclipse-temurin:21-jdk` stage, into `/opt/javac-worker.jar`. It uses
javac's task pool and platform provider from the `jdk.compiler` packages
`com.sun.tools.javac.api` and `com.sun.tools.javac.platform`, which the
jar's manifest exports to it. The jlink module set is `java.compiler`,
`jdk.compiler` (which carries `lib/ct.sym`, the API signatures of every
`--release` version) and `jdk.zipfs` (which reads `ct.sym`). The warm-up
sample is `/opt/javac-warmup/Warmup.java`.

**Launch.**

```
/opt/java/bin/java -XX:+UseSerialGC -Xmx<64 + 16 * workers>m -XX:-UsePerfData \
  -jar /opt/javac-worker.jar --threads <workers> --warmup /opt/javac-warmup/Warmup.java
```

with the environment of section 9.3. `-XX:-UsePerfData` keeps the JVM
from writing `hsperfdata` files. `--warmup` checks the sample once on each
pool thread before the first request line is read.

**One-shot.** `java -jar /opt/javac-worker.jar --once --release <N>
<file>...` checks the files against the working directory and prints the
report object below to stdout, exiting `0`; on an operational failure it
writes the cause to stderr and exits `1`. The java language's manifest
`command` runs it (through `cobe-lint lint java`).

**Lines.** The server writes requests to the worker's stdin and reads
replies from its stdout, one UTF-8 JSON object per `\n`-terminated line,
with nothing else on stdout. Stderr is free-form and the server copies it
to its log.

- Request: `{"id": 17, "release": "11", "dir": "/tmp/cobe-lint-ws-ab12",
  "files": ["/tmp/cobe-lint-ws-ab12/src/app/Solution.java"]}`. `id` is a
  positive integer unique within one worker process; `release` is a
  `--release` value; `dir` is the workspace and `files` holds absolute
  paths inside it.
- Success reply: `{"id": 17, "report": {"tool": "javac 21.0.12.1 --release
  11", "findings": [...]}}`. Each finding has the shape of section 2, with
  `path` as the request named the file. The one-shot report is the same
  object.
- Error reply: `{"id": 17, "error": "<message>"}` when the check cannot
  run: an unsupported release, an unreadable file, too many sources, or an
  error javac reports without a source.
- Exactly one reply per request, in any order. On stdin EOF the worker
  finishes the requests in flight and exits `0`.
- Strings in replies escape every character outside printable ASCII as
  `\uXXXX`, so a reply line is pure ASCII. The error text is the
  exception's cause chain, one `class: message` per line, the lines after
  the first prefixed `Caused by: `.
- A reply whose `id` the server did not send (for example one for a
  request the server already gave up on, from an earlier worker) is
  logged and ignored. A line that is not JSON, lacks a positive `id`,
  carries both or neither of `report` and `error`, or is longer than
  32 MiB is not a valid reply.

**Check.** Each pool thread keeps a javac task pool, which reuses the
symbol tables of earlier tasks and forgets every class a task's
compilation units declared, and per release a file manager over that
release's `ct.sym` signatures, built on first use. A request compiles the
named files first and the other sources after them, with `-source <N>
-target <N> -proc:none -implicit:none -encoding UTF-8`, empty class and
source paths, and up to 1000 errors and 1000 warnings. Because the named
files come first, a class a sibling declares again is reported as a
duplicate in the sibling. Error diagnostics map to `error`, warnings to
`warning`, and notes are dropped. Messages use the English locale and
keep javac's `symbol:` and `location:` lines. `rule` is the diagnostic
code. Columns count code points from the line start, a tab as one. A
range that continues past its first line ends at that line's end.
`System.out` is pointed at stderr at startup, so nothing javac prints can
reach the reply stream.

**Exit codes.** `0` after stdin EOF; `2` for bad arguments; `3` for a
request line it cannot parse; `4` on `OutOfMemoryError`, without a
reply, since the heap may be inconsistent; `5` when stdout is gone. Any
exit is a restart for the server, and the requests in flight answer
`503 linter_not_ready`.

**Process.** The server starts the worker in its own process group with
`/` as working directory and a parent-death signal of `SIGKILL`, and
kills the whole group when it restarts or poisons the worker.

**Mapping.** The server feeds a success reply to the javac driver as
`Parse(stdout = report, stderr = nil, exitCode = 0)` and an error reply
as `Parse(stdout = nil, stderr = error, exitCode = 1)`. The latter is an
operational failure, which the server answers as
`linter_execution_failed`, the outcome the one-shot CLI has for the same
failure. `linter.NormalizePath` then maps the absolute paths back to
`filename`.
