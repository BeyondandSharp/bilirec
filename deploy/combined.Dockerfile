# SPDX-License-Identifier: GPL-3.0-or-later
#
# All-in-one local image: bilirec backend + flvrepair sidecar + the React
# frontend served by nginx on the same origin (nginx proxies API calls to the
# backend on 127.0.0.1:8080).
#
#   docker build -f deploy/combined.Dockerfile -t local .
#   ./build.sh                 # same as above
#
# Tagged `local` by build.sh; for separate images see deploy/Dockerfile
# (local-backend) and deploy/web.Dockerfile (local-frontend).

ARG GO_IMAGE=golang:1.27-bookworm
ARG RUST_IMAGE=rust:1-slim-bookworm
ARG NODE_IMAGE=node:24-slim
ARG RUNTIME_IMAGE=python:3.13-slim
ARG FFMPEG_IMAGE=mwader/static-ffmpeg:7.1

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
# 2. Go backend + generated swagger docs
# ---------------------------------------------------------------------------
FROM ${GO_IMAGE} AS go-build

ARG GOPROXY_URL=https://goproxy.cn,direct
ARG SWAG_VERSION=v1.16.6

ENV GOPROXY=${GOPROXY_URL} \
    GOSUMDB=off \
    CGO_ENABLED=0

WORKDIR /src

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
# 3. Rust FLV repair sidecar
# ---------------------------------------------------------------------------
FROM ${RUST_IMAGE} AS rust-build

ENV CARGO_TERM_COLOR=never

WORKDIR /src
COPY rust/ ./rust/
RUN cargo build --locked --manifest-path rust/Cargo.toml --workspace --release \
    && install -D -m 0755 rust/target/release/flvrepair /out/flvrepair

# ---------------------------------------------------------------------------
# 4. Static ffmpeg binaries
# ---------------------------------------------------------------------------
FROM ${FFMPEG_IMAGE} AS ffmpeg-static

# ---------------------------------------------------------------------------
# 5. Runtime: nginx (frontend) + bilirec + ffmpeg/streamlink
# ---------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE} AS runtime

ARG STREAMLINK_VERSION=8.6.2

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        nginx \
        tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && python3 -m pip install --no-cache-dir --break-system-packages \
        "streamlink==${STREAMLINK_VERSION}" \
    && streamlink --version \
    && rm -f /etc/nginx/sites-enabled/default

COPY deploy/combined/nginx.conf /etc/nginx/conf.d/bilirec.conf
COPY deploy/combined/entrypoint.sh /usr/local/bin/bilirec-combined-entrypoint
RUN chmod 0755 /usr/local/bin/bilirec-combined-entrypoint \
    && mkdir -p /app/web /app/docs /data/records /data/database /data/secrets

COPY --from=web-build /web/dist /app/web
COPY --from=go-build /out/bilirec /usr/local/bin/bilirec
COPY --from=go-build /src/docs/swagger.json /app/docs/swagger.json
COPY --from=rust-build /out/flvrepair /usr/local/bin/flvrepair
COPY --from=ffmpeg-static /ffmpeg /usr/local/bin/ffmpeg
RUN chmod 0755 /usr/local/bin/ffmpeg && ffmpeg -version >/dev/null \
    && ln -sfn /app/docs /usr/local/bin/docs

# Same defaults as the backend-only image; the frontend is served on :80 and
# nginx proxies everything that is not a static file to 127.0.0.1:8080.
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
    FRONTEND_URLS=http://localhost,http://127.0.0.1 \
    BILIBILI_LOGIN_MODE=anonymous \
    TZ=UTC

WORKDIR /app
EXPOSE 80 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=25s --retries=3 \
    CMD ["python3", "-c", "import urllib.request,sys; urllib.request.urlopen('http://127.0.0.1/', timeout=4); r=urllib.request.urlopen('http://127.0.0.1/version', timeout=4); sys.exit(0 if r.status == 200 else 1)"]

STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/bilirec-combined-entrypoint"]
