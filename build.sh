#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Build convenience script for this repo.
#
# Images are exported as docker-loadable tar files under ./dist/ instead of
# being loaded into the local Docker image store. Default images are "slim":
# they contain no ffmpeg/streamlink (native recording only). Pass --full to
# include ffmpeg + streamlink (external downloaders + mp4 conversion); full
# images use the `-full` tag suffix.
#
#   ./build.sh                     slim all-in-one -> dist/local.tar
#   ./build.sh --full              full all-in-one -> dist/local-full.tar
#   ./build.sh --backend           slim backend   -> dist/local-backend.tar
#   ./build.sh --backend --full    full backend   -> dist/local-backend-full.tar
#   ./build.sh --frontend          slim frontend  -> dist/local-frontend.tar
#   ./build.sh --frontend --full   full frontend  -> dist/local-frontend-full.tar (same content)
#   ./build.sh --frontend-static   frontend static files into ./dist (no image)
#   ./build.sh --all               all three slim tars + ./dist static files
#   ./build.sh --all --full        all three full tars + ./dist static files
#
# Any combination of --backend / --frontend / --frontend-static builds only the
# selected artifacts and does NOT produce the combined tar. --all overrides
# selective flags and builds every artifact.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

OUT_DIR="$ROOT/dist"

usage() {
    sed -n '3,26p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
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
    suffix="-full"; extra=1
else
    suffix=""; extra=0
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
    local tag="$1" dockerfile="$2" out="$3"; shift 3
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
    export_image "local${suffix}" deploy/combined.Dockerfile "$OUT_DIR/local${suffix}.tar"
}

build_backend() {
    export_image "local-backend${suffix}" deploy/Dockerfile "$OUT_DIR/local-backend${suffix}.tar"
}

build_frontend() {
    # Frontend image contains only nginx + static files; the -full suffix is
    # kept for naming symmetry, its content is identical to the slim variant.
    export_image "local-frontend${suffix}" deploy/web.Dockerfile "$OUT_DIR/local-frontend${suffix}.tar"
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
[[ $full -eq 1 ]] && kind="full（含 ffmpeg/streamlink）"
echo
echo "完成：$kind，镜像已导出为 tar，不会留在本机 docker images 中。"
[[ $combined -eq 1 ]] && echo "  $OUT_DIR/local${suffix}.tar           (backend + frontend)"
[[ $backend  -eq 1 ]] && echo "  $OUT_DIR/local-backend${suffix}.tar   (backend only)"
[[ $frontend -eq 1 ]] && echo "  $OUT_DIR/local-frontend${suffix}.tar  (frontend only)"
[[ $static   -eq 1 ]] && echo "  静态文件: $OUT_DIR/"
if [[ $combined -eq 1 ]]; then
    echo
    echo "使用 all-in-one 镜像："
    echo "  docker load -i dist/local${suffix}.tar"
    echo "  docker run --rm -p 8080:80 -v bilirec-data:/data local${suffix}"
    echo "  打开 http://localhost:8080"
fi

exit 0
