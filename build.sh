#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Build convenience script for this repo.
#
# Images are exported as docker-loadable tar files under ./dist/ instead of
# being loaded into the local Docker image store.
#
#   ./build.sh                     all-in-one image (backend + frontend) -> dist/local.tar
#   ./build.sh --backend           backend-only image                  -> dist/local-backend.tar
#   ./build.sh --frontend          frontend-only image (nginx)         -> dist/local-frontend.tar
#   ./build.sh --frontend-static   frontend static files into ./dist (no image)
#   ./build.sh --all               build everything: all three tars + ./dist static files
#
# Any combination of --backend / --frontend / --frontend-static builds only the
# selected artifacts and does NOT produce the combined `local` tar. --all
# overrides selective flags and builds every artifact (including `local`).
#
# Load/build-run example:
#   docker load -i dist/local.tar
#   docker run --rm -p 8080:80 -v bilirec-data:/data local
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

OUT_DIR="$ROOT/dist"

usage() {
    sed -n '3,24p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

backend=0
frontend=0
static=0
all=0
selective=0

for arg in "$@"; do
    case "$arg" in
        --backend)
            backend=1; selective=1 ;;
        --frontend)
            frontend=1; selective=1 ;;
        --frontend-static)
            static=1; selective=1 ;;
        --all)
            all=1 ;;
        -h|--help)
            usage; exit 0 ;;
        *)
            echo "未知参数：$arg" >&2
            echo >&2
            usage >&2
            exit 2 ;;
    esac
done

if [[ $all -eq 1 ]]; then
    backend=1; frontend=1; static=1; combined=1
elif [[ $selective -eq 0 ]]; then
    # Default: the all-in-one image that ships both backend and frontend.
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

# Build an image and export it as a docker-loadable tar without loading it into
# the local image store. Prefers `docker buildx build --output type=docker,dest=`
# (docker's default buildx driver supports this and keeps the daemon clean);
# falls back to build + save + rmi on older setups.
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
            -t "$tag" \
            -f "$dockerfile" \
            "$ROOT"
    else
        docker build -t "$tag" -f "$dockerfile" "$ROOT"
        docker save -o "$out" "$tag"
        docker rmi "$tag" >/dev/null
    fi
}

build_combined() {
    export_image local deploy/combined.Dockerfile "$OUT_DIR/local.tar"
}

build_backend() {
    export_image local-backend deploy/Dockerfile "$OUT_DIR/local-backend.tar"
}

build_frontend() {
    export_image local-frontend deploy/web.Dockerfile "$OUT_DIR/local-frontend.tar"
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

# Order: independent artifacts first, all-in-one last (it is the slowest).
[[ $backend  -eq 1 ]] && build_backend
[[ $frontend -eq 1 ]] && build_frontend
[[ $static   -eq 1 ]] && build_frontend_static
[[ $combined -eq 1 ]] && build_combined

echo
echo "完成（镜像已导出为 tar，不会留在本机 docker images 中）："
[[ $combined -eq 1 ]] && echo "  $OUT_DIR/local.tar           (backend + frontend)"
[[ $backend  -eq 1 ]] && echo "  $OUT_DIR/local-backend.tar   (backend only)"
[[ $frontend -eq 1 ]] && echo "  $OUT_DIR/local-frontend.tar  (frontend only)"
[[ $static   -eq 1 ]] && echo "  静态文件: $OUT_DIR/"
if [[ $combined -eq 1 ]]; then
    echo
    echo "使用 all-in-one 镜像："
    echo "  docker load -i dist/local.tar"
    echo "  docker run --rm -p 8080:80 -v bilirec-data:/data local"
    echo "  打开 http://localhost:8080 ，登录页后端地址填 http://localhost:8080"
fi
