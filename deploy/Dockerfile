# syntax=docker/dockerfile:1.7
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Multi-stage image for bilirec + the Rust `flvrepair` sidecar.
#
# The repository root *is* the Go module (go.mod, cmd/, internal/, pkg/, utils/
# and the swagger.go/dotenv.go root files live there), so the build context is
# the repo root.
#
#   docker build -f deploy/Dockerfile -t bilirec-rust:dev .
#   docker compose -f deploy/docker-compose.yml up --build
#
# Stages:
#   go-build   Go backend, with swagger.json generated (gitignored in the repo,
#              see docs/INTERFACES.md §5). GO builder is CGO-free/static.
#   rust-build Rust `flvrepair` release binary (GPL-3.0-or-later).
#   runtime    Debian/Python slim + ffmpeg + streamlink, non-root, /data volumes.
#
# Build args are provided so CI mirrors can be substituted:
#   --build-arg GO_IMAGE=... --build-arg GOPROXY_URL=... --build-arg RUST_IMAGE=...
#   --build-arg RUNTIME_IMAGE=... --build-arg STREAMLINK_VERSION=...
#
# deploy/Dockerfile.dockerignore keeps the context small (excludes web/, refs/,
# testdata/, scripts/, backups/, dist/, docs/, deploy/ and rust/target).

ARG GO_IMAGE=golang:1.27-bookworm
ARG RUST_IMAGE=rust:1-slim-bookworm
ARG RUNTIME_IMAGE=python:3.13-slim

# ---------------------------------------------------------------------------
# 1. Go backend + generated swagger docs
# ---------------------------------------------------------------------------
FROM ${GO_IMAGE} AS go-build

ARG GOPROXY_URL=https://goproxy.cn,direct
ARG SWAG_VERSION=v1.16.6

ENV GOPROXY=${GOPROXY_URL} \
    GOSUMDB=off \
    CGO_ENABLED=0

WORKDIR /src

# Dependency layer first for better caching.
COPY go.mod go.sum ./
RUN go mod download

COPY . ./

# Swagger is generated at image build time (docs/ is gitignored). The generated
# docs package is imported by internal/modules/rest and the root package embeds
# docs/swagger.json, so seed a placeholder package first to let `swag init`
# parse the tree without warnings, then regenerate for real.
RUN go install github.com/swaggo/swag/cmd/swag@${SWAG_VERSION}

RUN mkdir -p docs \
    && printf 'package docs\n' > docs/docs.go \
    && : > docs/swagger.json \
    && "$(go env GOPATH)/bin/swag" init -g internal/modules/rest/rest.go -o docs \
    && test -s docs/swagger.json

RUN go build -trimpath -ldflags="-s -w" -o /out/bilirec ./cmd/backend

# ---------------------------------------------------------------------------
# 2. Rust FLV repair sidecar
# ---------------------------------------------------------------------------
FROM ${RUST_IMAGE} AS rust-build

ENV CARGO_TERM_COLOR=never

WORKDIR /src
COPY rust/ ./rust/
RUN cargo build --locked --manifest-path rust/Cargo.toml --workspace --release \
    && install -D -m 0755 rust/target/release/flvrepair /out/flvrepair

# ---------------------------------------------------------------------------
# 3. Runtime: ffmpeg + streamlink + both binaries, non-root
# ---------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE} AS runtime

ARG STREAMLINK_VERSION=8.6.2

ENV DEBIAN_FRONTEND=noninteractive

# ffmpeg is the external downloader fallback and the streamlink remux stage;
# streamlink is the hybrid downloader. tzdata keeps the Go logger configurable.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        ffmpeg \
        tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && python3 -m pip install --no-cache-dir --break-system-packages \
        "streamlink==${STREAMLINK_VERSION}" \
    && ffmpeg -version >/dev/null \
    && streamlink --version

# Non-root runtime user. /data holds records/database/secrets volumes.
RUN groupadd --gid 10001 bilirec \
    && useradd --uid 10001 --gid 10001 --home-dir /app --no-create-home \
        --shell /usr/sbin/nologin bilirec \
    && mkdir -p /app/docs /data/records /data/database /data/secrets \
    && chown -R bilirec:bilirec /app /data

COPY --from=go-build /out/bilirec /usr/local/bin/bilirec
COPY --from=go-build /src/docs/swagger.json /app/docs/swagger.json
COPY --from=rust-build /out/flvrepair /usr/local/bin/flvrepair
RUN chmod 0755 /usr/local/bin/bilirec /usr/local/bin/flvrepair \
    && ln -sfn /app/docs /usr/local/bin/docs

# Defaults reproduce native v0.3.1 behavior (docs/INTERFACES.md §2.3).
ENV PORT=8080 \
    HOST=0.0.0.0 \
    DOWNLOADER=auto \
    DOWNLOADER_FALLBACK=native \
    FFMPEG_BIN=ffmpeg \
    STREAMLINK_BIN=streamlink \
    FLV_REPAIR=auto \
    FLV_REPAIR_BIN=/usr/local/bin/flvrepair \
    FLV_REPAIR_MODE=best-effort \
    FLV_REPAIR_MAX_BUFFER_MB=64 \
    EXTERNAL_START_TIMEOUT_SECS=20 \
    OUTPUT_DIR=/data/records \
    DATABASE_DIR=/data/database \
    SECRET_DIR=/data/secrets \
    TZ=UTC

WORKDIR /app
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["python3", "-c", "import os,urllib.request,sys; r=urllib.request.urlopen('http://127.0.0.1:'+os.environ.get('PORT','8080')+'/version', timeout=4); sys.exit(0 if r.status == 200 else 1)"]

STOPSIGNAL SIGTERM
USER bilirec:bilirec
ENTRYPOINT ["/usr/local/bin/bilirec"]
