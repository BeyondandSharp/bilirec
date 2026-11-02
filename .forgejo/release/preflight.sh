#!/bin/sh
# Release preflight hook for bilirec.
#
# Runs from the reusable release Action (BeyondandSharp/action-template) before
# any Release is created, on the tree of the tag being released. The workflow
# has already checked out that tag; this script is the target repository's own
# "is this tag releasable?" check. Exit 0 = publish, non-zero = refuse
# (a repository variable RELEASE_PRECHECK_OVERRIDE=pass/skip can override it).
#
# bilirec has no second copy of the version number: the tag IS the version, and
# the build embeds it through
#   -X github.com/bilirec/bilirec/pkg/updatecheck.currentVersionInjected=<tag>
# So the checks below are about the tagged tree still being what the Release
# advertises (a tree that can build the documented images).
set -eu

cd "${GITHUB_WORKSPACE:-$(pwd)}"

version="${RELEASE_VERSION:-}"
tag="${RELEASE_TAG:-}"
expected_sha="${RELEASE_SHA:-}"

errors=0
ok()   { echo "[OK] $*"; }
err()  { echo "[ERROR] $*"; errors=$((errors + 1)); }
warn() { echo "[WARN] $*"; }

if [ -z "$version" ] || [ -z "$tag" ]; then
  echo "[ERROR] RELEASE_TAG / RELEASE_VERSION 未设置：本 hook 需由 release Action 运行" >&2
  exit 1
fi

echo "[preflight] tag=$tag version=$version prerelease=${RELEASE_PRERELEASE:-false}"

# 1. The version has one source of truth: the tag. Reject a tag/version
#    mismatch instead of publishing a Release under a surprising name.
case "$tag" in
  "v$version"|"$version") ok "tag 与 version 一致：$tag <-> $version" ;;
  *) err "tag '$tag' 与 version '$version' 不一致（应写成 v$version 或 $version）" ;;
esac

# 2. The workspace must be exactly the tagged commit (the generic Action checks
#    this too; repeated here so the hook is meaningful when run standalone).
if [ -n "$expected_sha" ]; then
  head_sha=$(git rev-parse HEAD 2>/dev/null || true)
  if [ "$head_sha" = "$expected_sha" ]; then
    ok "HEAD = $expected_sha（tag 指向的提交）"
  else
    err "HEAD（${head_sha:-<未知>}）不是 tag $tag 指向的提交（$expected_sha）"
  fi
else
  warn "RELEASE_SHA 未设置，跳过 HEAD 校验"
fi

# 3. The tagged tree must still contain the inputs the image workflows build
#    from; a Release pointing at a tree that cannot produce the images would be
#    advertising something that does not exist.
for path in \
  go.mod go.sum Makefile build.sh build-alpine.sh \
  cmd/backend rust/Cargo.toml rust/Cargo.lock \
  web/package.json web/pnpm-lock.yaml \
  deploy/alpine-combined.Dockerfile deploy/combined.Dockerfile \
  deploy/alpine.Dockerfile deploy/Dockerfile \
  pkg/updatecheck/updatecheck.go \
  .forgejo/workflows/docker-publish.yml \
  .forgejo/workflows/docker-publish-manual.yml
do
  if [ -e "$path" ]; then
    ok "存在 $path"
  else
    err "缺少 $path（tag 树不是可发布状态）"
  fi
done

# 4. Each published image Dockerfile embeds the release version through the
#    VERSION build arg (the docker-publish Action injects it automatically).
#    Missing it means images without a version / disabled update check.
for dockerfile in \
  deploy/alpine-combined.Dockerfile deploy/combined.Dockerfile \
  deploy/alpine.Dockerfile deploy/Dockerfile
do
  [ -f "$dockerfile" ] || continue  # already reported above when missing
  if grep -q '^ARG VERSION' "$dockerfile"; then
    ok "$dockerfile 声明了 ARG VERSION"
  else
    err "$dockerfile 缺少 ARG VERSION（构建出的镜像不会带 release 版本号）"
  fi
done

echo
if [ "$errors" -ne 0 ]; then
  echo "[ERROR] preflight 失败：$errors 项检查未通过" >&2
  echo "        临时放行：仓库变量 RELEASE_PRECHECK_OVERRIDE=pass（不建议长期开启）" >&2
  exit 1
fi
echo "[OK] preflight 通过"
