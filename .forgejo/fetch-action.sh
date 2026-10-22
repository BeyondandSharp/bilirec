#!/bin/sh
# fetch-action.sh — fetch the Action programs from the template repository.
#
# The target repository only carries this thin `.forgejo/` (workflow, this
# script, variants example). The programs that do the work live in the template
# repository and are fetched when a job starts, so "update the Action" is one
# variable (`ACTION_REF`), not a re-copy of the directory.
#
# Usage: sh fetch-action.sh [default-subdir]
#
# Configuration (mapped onto the step's `env:` by the workflow):
#   ACTION_REPO_URL          required — https://…/action-template.git, ssh://… or git@…
#   ACTION_REF               optional — branch, tag or commit SHA; empty = default branch
#   ACTION_SUBDIR            optional — overrides the positional default (fork/rename)
#   ACTION_TOKEN             optional — read-only token for a private HTTPS repository
#   ACTION_FETCH_ATTEMPTS    optional — total attempts, default 3 (max 5)
#   ACTION_FETCH_DELAY       optional — backoff unit in seconds, default 2
#
# The fetched programs replace `<forgejo_dir>/scripts` only; the workflow and the
# project's variants.txt are never touched. The old directory is swapped out as
# a whole, so programs deleted upstream disappear here too.
#
# Security: the token is masked in the log and handed to git through GIT_ASKPASS
# (a 0700 temporary file that references the environment, not the literal), so it
# never appears in the URL, the argv or a log line. The script never uses `set -x`.

set -u

DEFAULT_SUBDIR=${1:-}
REPO_URL=${ACTION_REPO_URL:-}
REF=${ACTION_REF:-}
SUBDIR=${ACTION_SUBDIR:-$DEFAULT_SUBDIR}
TOKEN=${ACTION_TOKEN:-}
ATTEMPTS=${ACTION_FETCH_ATTEMPTS:-3}
DELAY=${ACTION_FETCH_DELAY:-2}

info() { printf '%s\n' "$*"; }
warn() { printf '[WARN] %s\n' "$*" >&2; }
die() {
  printf 'fetch-action: %s\n' "$*" >&2
  exit 1
}

[ -n "$REPO_URL" ] || die "未配置 ACTION_REPO_URL（模板仓库地址），例如 https://git.example.com/owner/action-template.git
  或 ssh://git@git.example.com/owner/action-template.git；私有仓库再加 ACTION_TOKEN"
[ -n "$SUBDIR" ] || die "未指定 Action 子目录：把子目录名作为参数（如 docker-build-push），或设置 ACTION_SUBDIR"
command -v git >/dev/null 2>&1 || die "容器里没有 git：docker 版由 Bootstrap 安装，npm 版请使用自带 git 的镜像"

case $ATTEMPTS in '' | *[!0-9]*) ATTEMPTS=3 ;; esac
case $DELAY in '' | *[!0-9]*) DELAY=2 ;; esac
[ "$ATTEMPTS" -ge 1 ] || ATTEMPTS=1
[ "$ATTEMPTS" -le 5 ] || ATTEMPTS=5

FORGEJO_DIR=$(cd "$(dirname "$0")" && pwd) || die "无法定位 .forgejo 目录"
[ -n "$FORGEJO_DIR" ] || die "无法定位 .forgejo 目录"
TARGET=$FORGEJO_DIR/scripts

TMP_BASE=${RUNNER_TEMP:-/tmp}
WORK=$(mktemp -d "$TMP_BASE/action-fetch.XXXXXX") || die "无法在 $TMP_BASE 创建临时目录（检查 RUNNER_TEMP）"
ASKPASS=
STAGING=

cleanup() {
  [ -n "$WORK" ] && rm -rf "$WORK"
  [ -n "$ASKPASS" ] && rm -f "$ASKPASS"
  [ -n "$STAGING" ] && rm -rf "$STAGING"
  return 0
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

# Never let git block on an interactive prompt in a CI container.
GIT_TERMINAL_PROMPT=0
export GIT_TERMINAL_PROMPT
case $REPO_URL in
  ssh://* | git@*)
    GIT_SSH_COMMAND=${GIT_SSH_COMMAND:-'ssh -o BatchMode=yes'}
    export GIT_SSH_COMMAND
    ;;
esac

# A token must not reach the URL or argv: let git ask a helper script instead.
if [ -n "$TOKEN" ]; then
  printf '::add-mask::%s\n' "$TOKEN"
  export ACTION_TOKEN="$TOKEN"
  ASKPASS=$WORK/askpass.sh
  cat >"$ASKPASS" <<'ASKPASS_EOF'
#!/bin/sh
case $1 in
  [Uu]sername*) printf '%s\n' "${ACTION_GIT_USER:-oauth2}" ;;
  *) printf '%s\n' "$ACTION_TOKEN" ;;
esac
ASKPASS_EOF
  chmod 700 "$ASKPASS"
  GIT_ASKPASS=$ASKPASS
  export GIT_ASKPASS
fi

# A full/short commit SHA is not accepted by `git clone --branch`; fetch by ref.
is_commitish() {
  case $1 in
    '' | *[!0-9a-fA-F]*) return 1 ;;
  esac
  [ "${#1}" -ge 7 ] && [ "${#1}" -le 64 ]
}

clone_once() {
  rm -rf "$1"
  if is_commitish "$REF"; then
    mkdir -p "$1" || return 1
    git -C "$1" init -q || return 1
    git -C "$1" remote add origin "$REPO_URL" || return 1
    git -C "$1" fetch -q --depth 1 origin "$REF" || return 1
    git -C "$1" checkout -q --detach FETCH_HEAD || return 1
  elif [ -n "$REF" ]; then
    git clone -q --depth 1 --branch "$REF" "$REPO_URL" "$1"
  else
    git clone -q --depth 1 "$REPO_URL" "$1"
  fi
}

# Auth/ref/permission answers cannot be fixed by waiting; everything else may be
# a transport hiccup worth another attempt.
retryable() {
  grep -Eqi 'authentication failed|could not read username|repository not found|not found|permission denied|access denied|invalid username or password|terminal prompts disabled|does not appear to be a git repository' "$1" && return 1
  return 0
}

attempt=1
while :; do
  info "获取 Action 脚本：$REPO_URL@${REF:-<默认分支>} → $TARGET"
  if clone_once "$WORK/repo" 2>"$WORK/git.err"; then
    break
  fi
  if ! retryable "$WORK/git.err" || [ "$attempt" -ge "$ATTEMPTS" ]; then
    printf 'fetch-action: 拉取失败（第 %s/%s 次尝试）：%s\n' "$attempt" "$ATTEMPTS" "$REPO_URL" >&2
    sed -n '1,20p' "$WORK/git.err" >&2
    die "检查 ACTION_REPO_URL / ACTION_REF / ACTION_TOKEN 与网络连通性（仓库地址与 ref 是否正确？）"
  fi
  wait=$((DELAY * attempt))
  warn "拉取失败（第 $attempt/$ATTEMPTS 次尝试），${wait}s 后重试"
  [ "$wait" -gt 0 ] && sleep "$wait"
  attempt=$((attempt + 1))
done

SOURCE=$WORK/repo/$SUBDIR/scripts
if [ ! -f "$SOURCE/run.mjs" ]; then
  printf 'fetch-action: 拉取到的仓库里没有 %s/scripts/run.mjs（子目录或 ref 不对）\n' "$SUBDIR" >&2
  printf '  仓库根目录内容：\n' >&2
  ls -1 "$WORK/repo" 2>/dev/null | sed 's/^/    /' >&2 || true
  die "用 ACTION_SUBDIR（或脚本参数）指定正确的子目录，例如 docker-build-push / npm-publish"
fi

# Swap the directory as a whole: stale/removed programs must not survive an update.
STAGING=$FORGEJO_DIR/.scripts.fetch.$$
rm -rf "$STAGING"
cp -a "$SOURCE" "$STAGING" || die "复制脚本失败：$SOURCE → $STAGING"
rm -rf "$TARGET" || die "无法清理旧脚本目录：$TARGET"
mv "$STAGING" "$TARGET" || die "无法写入 $TARGET"
STAGING=

COMMIT=$(git -C "$WORK/repo" rev-parse --short HEAD 2>/dev/null || printf 'unknown')
COUNT=$(ls -1 "$TARGET" | wc -l | tr -d ' ')
info "已更新 Action 脚本（$COUNT 个文件，commit $COMMIT）：$TARGET"
