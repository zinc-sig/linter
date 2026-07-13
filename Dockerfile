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
# clang-tidy / Python toolchains.
FROM debian:13-slim

LABEL org.opencontainers.image.source="https://github.com/zinc-sig/linter" \
      org.opencontainers.image.description="Multi-language linter image (pylint, checkstyle, clang-tidy, go vet) for COBE sandbox lint runs"

# Fail piped RUNs (curl | tar) on the producer side too, not just the consumer.
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# Version pins generated from the languages/<lang> packages (see
# cmd/toolversions) — bump a pin by editing the language's const.
COPY --from=build /out/tool-versions.sh /opt/tool-versions.sh

RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 \
    python3-pip \
    default-jre-headless \
    clang-tidy \
    curl \
    ca-certificates \
    coreutils \
    && rm -rf /var/lib/apt/lists/*

# hadolint ignore=SC1091
RUN . /opt/tool-versions.sh \
    && pip3 install --no-cache-dir --break-system-packages "pylint==${PYLINT_VERSION}" \
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
