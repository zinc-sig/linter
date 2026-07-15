# syntax=docker/dockerfile:1

# --- build stage: static cobe-lint CLI (docs/CONTRACT.md) --------------------
FROM golang:1.25 AS build

WORKDIR /src
COPY go.mod ./
COPY linter/ linter/
COPY languages/ languages/
COPY cmd/ cmd/
# Toolchain pins live as consts in the languages/<lang> packages;
# cmd/toolversions exports them for the runtime stage's install steps.
# DL3062 misfires here: ./cmd/toolversions is a local package in this
# module, not a remote `go install` target one could pin.
# hadolint ignore=DL3062
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cobe-lint ./cmd/cobe-lint \
    && go run ./cmd/toolversions > /out/tool-versions.sh

# --- fetch stage: checkstyle jar (used by the jre and runtime stages) --------
FROM debian:13-slim AS fetch

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/tool-versions.sh /opt/tool-versions.sh

# hadolint ignore=SC1091
RUN . /opt/tool-versions.sh \
    && mkdir -p /out \
    && curl -fsSL "https://github.com/checkstyle/checkstyle/releases/download/checkstyle-${CHECKSTYLE_VERSION}/checkstyle-${CHECKSTYLE_VERSION}-all.jar" \
    -o /out/checkstyle.jar

# --- jre stage: minimal jlink'ed Java runtime for checkstyle ------------------
# eclipse-temurin:21-jdk: a pinned, multi-arch (amd64+arm64) JDK shipping
# jdeps/jlink; only the jlink'ed runtime below is copied onward, so the JDK
# bulk never reaches the final image.
FROM eclipse-temurin:21-jdk AS jre

COPY --from=fetch /out/checkstyle.jar /checkstyle.jar

# Derive the module set checkstyle actually uses, then build a stripped
# runtime containing exactly those modules (output kept off /opt/java,
# where this base image parks its own JDK).
RUN jdeps --print-module-deps --ignore-missing-deps --multi-release 21 /checkstyle.jar > /modules \
    && echo "checkstyle modules: $(cat /modules)" \
    && jlink --add-modules "$(cat /modules)" \
    --strip-debug --no-header-files --no-man-pages --compress=zip-6 \
    --output /jlinked

# --- ruff stage: pinned ruff binary for the python<NN> languages -------------
# Runs in its own stage so uv, its install scratch, and the tool venv never
# reach the final image; only the single ruff binary is copied onward.
FROM debian:13-slim AS ruff

# uv installs the pinned ruff below. Copied from the official image —
# pinned by tag, never `latest`, for reproducibility — whose multi-arch
# manifest covers both amd64 and arm64 builds without a curl|sh bootstrap;
# uv resolves the matching ruff wheel per build platform.
COPY --from=ghcr.io/astral-sh/uv:0.11.28 /uv /usr/local/bin/uv

# ruff itself is a native binary with no Python dependency; the throwaway
# interpreter uv auto-installs only assembles the tool venv and stays in
# this stage. Everything lands under /opt (not uv's /root defaults) so the
# copy below has a stable source; no download cache is kept.
ENV UV_NO_CACHE=1 \
    UV_PYTHON_INSTALL_DIR=/opt/python-scratch \
    UV_TOOL_DIR=/opt/uv-tools \
    UV_TOOL_BIN_DIR=/opt/uv-bin

COPY --from=build /out/tool-versions.sh /opt/tool-versions.sh

# hadolint ignore=SC1091
RUN . /opt/tool-versions.sh \
    && uv tool install "ruff==${RUFF_VERSION}"

# --- runtime stage -----------------------------------------------------------
# Pinned to the Debian 13 (trixie) slim variant rather than the floating
# `stable-slim` tag so a Debian major release can't silently change the
# clang-tidy toolchain. No Python interpreter ships at all — ruff is a
# native binary and each python<NN> package selects its dialect via
# --target-version — and Java doesn't come from Debian either (the jre
# stage jlinks a minimal runtime for checkstyle).
FROM debian:13-slim

LABEL org.opencontainers.image.source="https://github.com/zinc-sig/linter" \
      org.opencontainers.image.description="Multi-language linter image (ruff, checkstyle, clang-tidy, go vet) for COBE sandbox lint runs"

# Fail piped RUNs (curl | tar) on the producer side too, not just the consumer.
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# Version pins generated from the languages/<lang> packages (see
# cmd/toolversions) — bump a pin by editing the language's const.
COPY --from=build /out/tool-versions.sh /opt/tool-versions.sh

# Docs, man pages, and locales are dead weight in a headless lint image;
# purge them in the same layer that installs the packages. Two big
# clang-tools byproducts go too: c-index-test (a libclang test harness) and
# libclang's C API library — clang-tidy links only libclang-cpp/libLLVM
# (ldd-verified), neither of these (the *-linux-gnu glob keeps the removal
# multi-arch).
RUN apt-get update && apt-get install -y --no-install-recommends \
    clang-tidy \
    curl \
    ca-certificates \
    coreutils \
    && rm -rf /var/lib/apt/lists/* \
    /usr/share/doc /usr/share/man /usr/share/info /usr/share/locale \
    /usr/lib/llvm-19/bin/c-index-test \
    /usr/lib/*-linux-gnu/libclang-19.so*

# Minimal Java runtime and the checkstyle jar from the earlier stages.
COPY --from=jre /jlinked /opt/java
COPY --from=fetch /out/checkstyle.jar /opt/checkstyle.jar

# The pinned ruff binary — a self-contained native executable (the tool
# venv it was installed into stays behind in the ruff stage) — at the
# stable path languages/internal/ruff references.
COPY --from=ruff /opt/uv-tools/ruff/bin/ruff /usr/local/bin/ruff

COPY checkstyle-config.xml /opt/checkstyle-config.xml

ARG TARGETARCH
# `go vet` rebuilds stdlib export data from source, so src/ and pkg/ must
# stay; the toolchain's own test corpus, api metadata, and docs are dead
# weight here.
# hadolint ignore=SC1091
RUN . /opt/tool-versions.sh \
    && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" | tar -xz -C /usr/local \
    && rm -rf /usr/local/go/test /usr/local/go/api /usr/local/go/doc

ENV PATH="/usr/local/go/bin:${PATH}"

# Unified linter CLI (docs/CONTRACT.md): `cobe-lint manifest` describes the
# supported languages; `cobe-lint lint <language> <file>...` runs the native
# tool and normalizes its output to the unified findings JSON.
COPY --from=build /out/cobe-lint /usr/local/bin/cobe-lint

RUN mkdir -p /workspace && useradd -m -s /bin/false linter

USER linter
