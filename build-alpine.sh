#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Alpine/musl build script (sibling of build.sh; same flags, different
# Dockerfiles; images are exported as docker-loadable tar files under ./dist/).
# Default images are "slim": no ffmpeg/streamlink (native recording only).
# Pass --full to include the apk ffmpeg + streamlink packages; full images use
# the `-full` tag suffix.
#
#   ./build-alpine.sh                     slim all-in-one -> dist/local-alpine.tar
#   ./build-alpine.sh --full              full all-in-one -> dist/local-alpine-full.tar
#   ./build-alpine.sh --backend           slim backend   -> dist/local-backend-alpine.tar
#   ./build-alpine.sh --backend --full    full backend   -> dist/local-backend-alpine-full.tar
#   ./build-alpine.sh --frontend          slim frontend  -> dist/local-frontend-alpine.tar
#   ./build-alpine.sh --frontend --full   full frontend  -> dist/local-frontend-alpine-full.tar
#   ./build-alpine.sh --frontend-static   frontend static files into ./dist (no image)
#   ./build-alpine.sh --all               all three slim tars + ./dist static files
#   ./build-alpine.sh --all --full        all three full tars + ./dist static files
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

OUT_DIR="$ROOT/dist"

usage() {
    sed -n '3,25p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

backend=0
frontend=0
static=0
all=0
full=0
selective=0

for arg in "$@"; do
    case "$arg" in
        --backend) backend=1; selective=1 ;;
        --frontend) frontend=1; selective=1 ;;
        --frontend-static) static=1; selective=1 ;;
        --all) all=1 ;;
        --full) full=1 ;;
        -h|--help) usage; exit 0 ;;
        *)
            echo "未知参数：$arg" >&2
            echo >&2
            usage >&2
            exit 2 ;;
    esac
done

if [[ $full -eq 1 ]]; then
    extra=1
else
    extra=0
fi

if [[ $all -eq 1 ]]; then
    backend=1; frontend=1; static=1; combined=1
elif [[ $selective -eq 0 ]]; then
    combined=1
else
    combined=0
fi

need_docker() {
    command -v docker >/dev/null 2>&1 || {
        echo "错误：未找到 docker 命令" >&2
        exit 1
    }
}

BUILDX=0
if docker buildx version >/dev/null 2>&1; then
    BUILDX=1
fi

export_image() {
    local tag="$1" dockerfile="$2" out="$3"
    need_docker
    mkdir -p "$OUT_DIR"
    echo "==> 构建 $tag 并导出到 ${out#"$ROOT"/}"
    if [[ $BUILDX -eq 1 ]]; then
        docker buildx build \
            --output "type=docker,dest=$out" \
            --build-arg "WITH_EXTRA=$extra" \
            -t "$tag" \
            -f "$dockerfile" \
            "$ROOT"
    else
        docker build --build-arg "WITH_EXTRA=$extra" -t "$tag" -f "$dockerfile" "$ROOT"
        docker save -o "$out" "$tag"
        docker rmi "$tag" >/dev/null
    fi
}

build_combined() {
    local sfx=""; [[ $extra -eq 1 ]] && sfx="-full"
    export_image "local-alpine${sfx}" deploy/alpine-combined.Dockerfile "$OUT_DIR/local-alpine${sfx}.tar"
}

build_backend() {
    local sfx=""; [[ $extra -eq 1 ]] && sfx="-full"
    export_image "local-backend-alpine${sfx}" deploy/alpine.Dockerfile "$OUT_DIR/local-backend-alpine${sfx}.tar"
}

build_frontend() {
    local sfx=""; [[ $extra -eq 1 ]] && sfx="-full"
    # Frontend-only image has no ffmpeg/streamlink either way; suffix kept for
    # naming symmetry.
    export_image "local-frontend-alpine${sfx}" deploy/alpine-web.Dockerfile "$OUT_DIR/local-frontend-alpine${sfx}.tar"
}

build_frontend_static() {
    command -v pnpm >/dev/null 2>&1 || {
        echo "错误：--frontend-static 需要 pnpm（Node 24+）" >&2
        exit 1
    }
    echo "==> 构建前端静态文件到 ./dist"
    (
        cd web
        if [[ ! -d node_modules ]]; then
            pnpm install --frozen-lockfile
        fi
        pnpm build
    )
    mkdir -p "$OUT_DIR"
    cp -a "$ROOT/web/dist/." "$OUT_DIR/"
    echo "    静态文件已输出到 $OUT_DIR"
}

[[ $backend  -eq 1 ]] && build_backend
[[ $frontend -eq 1 ]] && build_frontend
[[ $static   -eq 1 ]] && build_frontend_static
[[ $combined -eq 1 ]] && build_combined

kind="slim（无 ffmpeg/streamlink）"
[[ $extra -eq 1 ]] && kind="full（含 apk ffmpeg/streamlink）"
echo
echo "完成：$kind，镜像已导出为 tar，不会留在本机 docker images 中。"
if [[ $combined -eq 1 ]]; then
    [[ $extra -eq 1 ]] && f="-full" || f=""
    echo "  $OUT_DIR/local-alpine${f}.tar           (backend + frontend, musl)"
    echo
    echo "使用 all-in-one alpine 镜像："
    echo "  docker load -i dist/local-alpine${f}.tar"
    echo "  docker run --rm -p 8080:80 -v bilirec-data:/data local-alpine${f}"
    echo "  打开 http://localhost:8080"
fi
if [[ $backend -eq 1 ]]; then
    [[ $extra -eq 1 ]] && f="-full" || f=""
    echo "  $OUT_DIR/local-backend-alpine${f}.tar   (backend only, musl)"
fi
if [[ $frontend -eq 1 ]]; then
    [[ $extra -eq 1 ]] && f="-full" || f=""
    echo "  $OUT_DIR/local-frontend-alpine${f}.tar  (frontend only)"
fi
[[ $static -eq 1 ]] && echo "  静态文件: $OUT_DIR/"
