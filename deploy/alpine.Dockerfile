# SPDX-License-Identifier: GPL-3.0-or-later
#
# Alpine/musl backend-only image (Go static binary + Rust musl binary +
# apk ffmpeg/streamlink). Exported as dist/local-backend-alpine.tar by
# build-alpine.sh.
#
# ffmpeg choice: Alpine's `apk` ffmpeg is used (measured ~4MB smaller than
# copying the 129MB static ffmpeg binary, and it is native musl).

ARG GO_IMAGE=golang:1.27-alpine
ARG RUST_IMAGE=rust:1-alpine
ARG RUNTIME_IMAGE=alpine:3.24

# ---------------------------------------------------------------------------
# 1. Go backend + generated swagger docs (CGO-free / musl compatible)
# ---------------------------------------------------------------------------
FROM ${GO_IMAGE} AS go-build

ARG GOPROXY_URL=https://goproxy.cn,direct
ARG SWAG_VERSION=v1.16.6

ENV GOPROXY=${GOPROXY_URL} \
    GOSUMDB=off \
    CGO_ENABLED=0

WORKDIR /src

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

RUN go install github.com/swaggo/swag/cmd/swag@${SWAG_VERSION}

RUN mkdir -p docs \
    && printf 'package docs\n' > docs/docs.go \
    && : > docs/swagger.json \
    && "$(go env GOPATH)/bin/swag" init -g internal/modules/rest/rest.go -o docs \
    && test -s docs/swagger.json

RUN go build -trimpath -ldflags="-s -w" -o /out/bilirec ./cmd/backend

# ---------------------------------------------------------------------------
# 2. Rust sidecar for musl (host target of rust:alpine is x86_64-unknown-linux-musl)
# ---------------------------------------------------------------------------
FROM ${RUST_IMAGE} AS rust-build

ENV CARGO_TERM_COLOR=never

WORKDIR /src
COPY rust/ ./rust/
RUN cargo build --locked --manifest-path rust/Cargo.toml --workspace --release \
    && install -D -m 0755 rust/target/release/flvrepair /out/flvrepair

# ---------------------------------------------------------------------------
# 3. Runtime: alpine + apk ffmpeg/streamlink + both binaries, non-root
# ---------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE} AS runtime

ARG WITH_EXTRA=0

RUN apk add --no-cache \
        ca-certificates \
        tzdata \
    && if [ "$WITH_EXTRA" = "1" ]; then \
         apk add --no-cache ffmpeg streamlink; \
       fi \
    && addgroup -g 10001 bilirec \
    && adduser -D -u 10001 -G bilirec -h /app bilirec \
    && mkdir -p /app/docs /data/records /data/database /data/secrets \
    && chown -R bilirec:bilirec /app /data

COPY --from=go-build /out/bilirec /usr/local/bin/bilirec
COPY --from=go-build /src/docs/swagger.json /app/docs/swagger.json
COPY --from=rust-build /out/flvrepair /usr/local/bin/flvrepair
RUN chmod 0755 /usr/local/bin/bilirec /usr/local/bin/flvrepair \
    && ln -sfn /app/docs /usr/local/bin/docs \
    && if [ "$WITH_EXTRA" = "1" ]; then \
         ffmpeg -version >/dev/null && streamlink --version >/dev/null; \
       fi

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
    CMD wget -q -O /dev/null http://127.0.0.1:8080/version || exit 1

STOPSIGNAL SIGTERM
USER bilirec:bilirec
ENTRYPOINT ["/usr/local/bin/bilirec"]
