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

# --- runtime stage -----------------------------------------------------------
# Pinned to the Debian 13 (trixie) slim variant rather than the floating
# `stable-slim` tag so a Debian major release can't silently change the
# clang-tidy toolchain. Python deliberately does NOT come from Debian: the
# python<NN> language packages pin exact CPython releases installed below.
FROM debian:13-slim

LABEL org.opencontainers.image.source="https://github.com/zinc-sig/linter" \
      org.opencontainers.image.description="Multi-language linter image (pylint, checkstyle, clang-tidy, go vet) for COBE sandbox lint runs"

# Fail piped RUNs (curl | tar) on the producer side too, not just the consumer.
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# Version pins generated from the languages/<lang> packages (see
# cmd/toolversions) — bump a pin by editing the language's const.
COPY --from=build /out/tool-versions.sh /opt/tool-versions.sh

RUN apt-get update && apt-get install -y --no-install-recommends \
    default-jre-headless \
    clang-tidy \
    curl \
    ca-certificates \
    coreutils \
    && rm -rf /var/lib/apt/lists/*

# uv installs the pinned CPython interpreters below. Copied from the
# official image — pinned by tag, never `latest`, for reproducibility —
# whose multi-arch manifest covers both amd64 and arm64 builds without a
# curl|sh bootstrap.
COPY --from=ghcr.io/astral-sh/uv:0.11.28 /uv /usr/local/bin/uv

# Managed interpreters must live outside /root (0700) so the non-root
# linter user can reach them; no download cache is kept in the image.
ENV UV_PYTHON_INSTALL_DIR=/opt/python-interpreters \
    UV_NO_CACHE=1

# One interpreter + pylint virtualenv per python<NN> language pin, at the
# stable /opt/python/<version> paths that languages/internal/pylint
# derives. Everything is baked at build time; lint runs stay offline.
# SC2086: ${PYTHON_VERSIONS} is a space-separated list — splitting is the
# point.
# hadolint ignore=SC1091,SC2086
RUN . /opt/tool-versions.sh \
    && for ver in ${PYTHON_VERSIONS}; do \
    uv python install "${ver}" \
    && uv venv --python "${ver}" "/opt/python/${ver}" \
    && uv pip install --python "/opt/python/${ver}/bin/python" "pylint==${PYLINT_VERSION}" \
    || exit 1; \
    done \
    && curl -fsSL "https://github.com/checkstyle/checkstyle/releases/download/checkstyle-${CHECKSTYLE_VERSION}/checkstyle-${CHECKSTYLE_VERSION}-all.jar" \
    -o /opt/checkstyle.jar

COPY checkstyle-config.xml /opt/checkstyle-config.xml

ARG TARGETARCH
# hadolint ignore=SC1091
RUN . /opt/tool-versions.sh \
    && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" | tar -xz -C /usr/local

ENV PATH="/usr/local/go/bin:${PATH}"

# Unified linter CLI (docs/CONTRACT.md): `cobe-lint manifest` describes the
# supported languages; `cobe-lint lint <language> <file>...` runs the native
# tool and normalizes its output to the unified findings JSON.
COPY --from=build /out/cobe-lint /usr/local/bin/cobe-lint

RUN mkdir -p /workspace && useradd -m -s /bin/false linter

USER linter
