#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Build convenience script for this repo.
#
#   ./build.sh                     all-in-one image (backend + frontend), tag `local`
#   ./build.sh --backend           backend-only image, tag `local-backend`
#   ./build.sh --frontend          frontend-only image (nginx), tag `local-frontend`
#   ./build.sh --frontend-static   frontend static files into ./dist (no image)
#   ./build.sh --all               build everything: local + local-backend +
#                                  local-frontend + ./dist static files
#
# Any combination of --backend / --frontend / --frontend-static builds only the
# selected artifacts and does NOT produce the combined `local` image. --all
# overrides selective flags and builds every artifact (including `local`).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

usage() {
    sed -n '3,22p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
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

build_combined() {
    need_docker
    echo "==> 构建 all-in-one 镜像：local（后端 + 前端，同源 nginx 代理）"
    docker build -f deploy/combined.Dockerfile -t local "$ROOT"
}

build_backend() {
    need_docker
    echo "==> 构建后端镜像：local-backend"
    docker build -f deploy/Dockerfile -t local-backend "$ROOT"
}

build_frontend() {
    need_docker
    echo "==> 构建前端镜像：local-frontend（nginx + 静态资源）"
    docker build -f deploy/web.Dockerfile -t local-frontend "$ROOT"
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
    mkdir -p "$ROOT/dist"
    cp -a "$ROOT/web/dist/." "$ROOT/dist/"
    echo "    静态文件已输出到 $ROOT/dist"
}

# Order: independent artifacts first, all-in-one last (it is the slowest and
# its Docker layers are independent from the other builds).
[[ $backend  -eq 1 ]] && build_backend
[[ $frontend -eq 1 ]] && build_frontend
[[ $static   -eq 1 ]] && build_frontend_static
[[ $combined -eq 1 ]] && build_combined

echo
echo "完成："
[[ $combined -eq 1 ]] && echo "  docker image: local          (backend + frontend)"
[[ $backend  -eq 1 ]] && echo "  docker image: local-backend  (backend only)"
[[ $frontend -eq 1 ]] && echo "  docker image: local-frontend (frontend only)"
[[ $static   -eq 1 ]] && echo "  static files: ./dist"
if [[ $combined -eq 1 ]]; then
    echo
    echo "运行 all-in-one 镜像示例（前端与 API 同源）："
    echo "  docker run --rm -p 8080:80 -v bilirec-data:/data local"
    echo "  打开 http://localhost:8080 ，登录页后端地址填 http://localhost:8080"
    echo "  如需直接访问 API，可额外加 -p 8081:8080"
fi
