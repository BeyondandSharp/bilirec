# SPDX-License-Identifier: GPL-3.0-or-later
#
# Alpine/musl all-in-one image: backend (Go static) + flvrepair (Rust musl) +
# frontend (React build) served by nginx on :80, API proxied to :8080.
# Exported as dist/local-alpine.tar by build-alpine.sh.
#
# ffmpeg choice: Alpine's `apk` ffmpeg (native musl, ~4MB smaller than the
# 129MB static ffmpeg binary). streamlink is also the native apk package.

ARG GO_IMAGE=golang:1.27-alpine
ARG RUST_IMAGE=rust:1-alpine
ARG NODE_IMAGE=node:24-alpine
ARG RUNTIME_IMAGE=alpine:3.24

# ---------------------------------------------------------------------------
# 1. Frontend static build
# ---------------------------------------------------------------------------
FROM ${NODE_IMAGE} AS web-build

ARG PNPM_VERSION=11.1.2
ENV PNPM_HOME=/usr/local/pnpm
ENV PATH=$PNPM_HOME:$PATH

WORKDIR /web
RUN npm install -g pnpm@${PNPM_VERSION}

COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile

COPY web/ ./
RUN pnpm build

# ---------------------------------------------------------------------------
# 2. Go backend + swagger
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
# 3. Rust sidecar (musl)
# ---------------------------------------------------------------------------
FROM ${RUST_IMAGE} AS rust-build

ENV CARGO_TERM_COLOR=never

WORKDIR /src
COPY rust/ ./rust/
RUN cargo build --locked --manifest-path rust/Cargo.toml --workspace --release \
    && install -D -m 0755 rust/target/release/flvrepair /out/flvrepair

# ---------------------------------------------------------------------------
# 4. Runtime: nginx + bilirec + apk ffmpeg/streamlink
# ---------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE} AS runtime

ARG WITH_EXTRA=0

RUN apk add --no-cache \
        bash \
        ca-certificates \
        nginx \
        tzdata \
    && if [ "$WITH_EXTRA" = "1" ]; then \
         apk add --no-cache ffmpeg streamlink; \
       fi \
    && rm -f /etc/nginx/http.d/default.conf \
    && mkdir -p /app/web /app/docs /app/records /app/database /app/secrets

COPY deploy/combined/nginx.conf /etc/nginx/http.d/bilirec.conf
COPY deploy/combined/entrypoint.sh /usr/local/bin/bilirec-combined-entrypoint
RUN chmod 0755 /usr/local/bin/bilirec-combined-entrypoint

COPY --from=web-build /web/dist /app/web
COPY --from=go-build /out/bilirec /usr/local/bin/bilirec
COPY --from=go-build /src/docs/swagger.json /app/docs/swagger.json
COPY --from=rust-build /out/flvrepair /usr/local/bin/flvrepair
RUN chmod 0755 /usr/local/bin/bilirec /usr/local/bin/flvrepair \
    && ln -sfn /app/docs /usr/local/bin/docs \
    && if [ "$WITH_EXTRA" = "1" ]; then \
         ffmpeg -version >/dev/null && streamlink --version >/dev/null; \
       fi

ENV PORT=8080 \
    DOWNLOADER=auto \
    DOWNLOADER_FALLBACK=native \
    FFMPEG_BIN=ffmpeg \
    STREAMLINK_BIN=streamlink \
    FLV_REPAIR=auto \
    FLV_REPAIR_BIN=/usr/local/bin/flvrepair \
    FLV_REPAIR_MODE=best-effort \
    FLV_REPAIR_MAX_BUFFER_MB=64 \
    EXTERNAL_START_TIMEOUT_SECS=20 \
    OUTPUT_DIR=records \
    DATABASE_DIR=database \
    SECRET_DIR=secrets \
    FRONTEND_URLS=https://app.bilirec.org \
    TZ=UTC

WORKDIR /app
EXPOSE 80 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=25s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1/version || exit 1

STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/bilirec-combined-entrypoint"]
