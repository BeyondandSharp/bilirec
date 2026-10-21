#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
#
# Unified POSIX-sh entrypoint for the bilirec backend and combined images
# (CONFIG-V2 §7). It is used by both the Debian and Alpine variants and by the
# backend-only and combined (nginx + bilirec) images.
#
# Usage (set by the image ENTRYPOINT; $1 is the container mode):
#   docker-entrypoint.sh backend  [command...]   # exec the command as PUID:PGID
#   docker-entrypoint.sh combined                # nginx (root) + bilirec (PUID)
#
# Responsibilities:
#   1. Validate PUID/PGID (numeric, default 10001).
#   2. Adjust the `bilirec` user/group ids to PUID/PGID using
#      `groupmod -o` / `usermod -o` (LinuxServer.io-style non-unique ids), so a
#      host gid such as 100 (the image's `users` group) is accepted.
#   3. Create the runtime dirs: /app/config, /app/secrets, /app/database,
#      /app/records. Never walk mounted trees: fix only the mount point itself
#      when PUID/PGID cannot write it, and exact config/secrets/*.db files when
#      the target user cannot read/write them. New files are created by the app
#      as PUID with umask 0022 (0644 files / 0755 dirs).
#   4. Drop privileges with gosu (Debian) or su-exec (Alpine). Backend images
#      exec the app as PID 1; combined images keep nginx running as root and run
#      the bilirec child as PUID:PGID, preserving the existing signal handling.
#
# Environment:
#   PUID / PGID           target uid/gid (default 10001)
#   CONFIG_FILE           default /app/config/config.json
#   SECRETS_FILE          default /app/config/secrets.json
#   BILIREC_BIN           app binary for `combined` mode (default /usr/local/bin/bilirec)
#   BILIREC_ENTRYPOINT_DRY_RUN=1  validate/chown only, do not start processes
set -eu

# Deterministic new-file modes: 0644 files / 0755 dirs for recordings and DBs.
umask 0022

BILIREC_USER=bilirec
BILIREC_GROUP=bilirec
APP_DIR=/app
CONFIG_DIR=/app/config
SECRETS_DIR=/app/secrets
DATABASE_DIRECTORY=/app/database
RECORDS_DIR=/app/records
CONFIG_FILE=${CONFIG_FILE:-/app/config/config.json}
SECRETS_FILE=${SECRETS_FILE:-/app/config/secrets.json}

log() { printf '[entrypoint] %s\n' "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

is_uint() {
    case "$1" in
        '' | *[!0-9]*) return 1 ;;
        *) return 0 ;;
    esac
}

PUID=${PUID:-10001}
PGID=${PGID:-10001}
is_uint "$PUID" || die "PUID/PGID must be numeric (got PUID='$PUID', PGID='$PGID')"
is_uint "$PGID" || die "PUID/PGID must be numeric (got PUID='$PUID', PGID='$PGID')"

MODE=${1:-backend}
if [ "$#" -gt 0 ]; then
    shift
fi

# ---------------------------------------------------------------------------
# Privilege tool + platform detection
# ---------------------------------------------------------------------------
if command -v gosu >/dev/null 2>&1; then
    PRIV=gosu
elif command -v su-exec >/dev/null 2>&1; then
    PRIV=su-exec
else
    die "neither gosu nor su-exec is installed (backend/combined images require one)"
fi

command -v groupmod >/dev/null 2>&1 && command -v usermod >/dev/null 2>&1 \
    || die "cannot adjust users/groups: shadow groupmod/usermod not installed"

CURRENT_UID=$(id -u "$BILIREC_USER" 2>/dev/null || true)
CURRENT_GID=$(id -g "$BILIREC_USER" 2>/dev/null || true)
[ -n "$CURRENT_UID" ] || die "image is broken: user '$BILIREC_USER' not found"
[ -n "$CURRENT_GID" ] || die "image is broken: group '$BILIREC_GROUP' not found"

# ---------------------------------------------------------------------------
# Adjust bilirec user/group ids (LinuxServer.io abc mapping).
#
# `-o` (non-unique) is deliberate: the image already owns gid 100 as `users`
# and may own other host ids. Letting the bilirec group/user reuse that numeric
# id is exactly what LinuxServer.io's groupmod/usermod -o does; the app still
# runs with the requested numeric PUID:PGID and files get that ownership.
# ---------------------------------------------------------------------------
if [ "$CURRENT_GID" != "$PGID" ]; then
    log "adjusting group $BILIREC_GROUP gid $CURRENT_GID -> $PGID (non-unique allowed)"
    groupmod -o -g "$PGID" "$BILIREC_GROUP" \
        || die "failed to change gid of $BILIREC_GROUP to $PGID"
fi
if [ "$CURRENT_UID" != "$PUID" ]; then
    USERHOME=$(awk -F: -v n="$BILIREC_USER" '$1 == n { print $6; exit }' /etc/passwd)
    [ -n "$USERHOME" ] || USERHOME=/app
    log "adjusting user $BILIREC_USER uid $CURRENT_UID -> $PUID (non-unique allowed)"
    # usermod -u recursively chowns the user's home directory (/app), which
    # would walk a potentially huge records/ tree. Point the home at a
    # nonexistent dir during the id change; the explicit chown below handles
    # /app, matching LinuxServer.io's `usermod -d /root` trick.
    usermod -d /nonexistent "$BILIREC_USER" || die "failed to detach home dir of $BILIREC_USER"
    usermod -o -u "$PUID" "$BILIREC_USER" || die "failed to change uid of $BILIREC_USER to $PUID"
    usermod -d "$USERHOME" "$BILIREC_USER" || die "failed to restore home dir of $BILIREC_USER"
fi

[ "$(id -u "$BILIREC_USER")" = "$PUID" ] || die "uid adjustment failed (got $(id -u "$BILIREC_USER"), want $PUID)"
[ "$(id -g "$BILIREC_USER")" = "$PGID" ] || die "gid adjustment failed (got $(id -g "$BILIREC_USER"), want $PGID)"

# ---------------------------------------------------------------------------
# Runtime dirs + ownership (never walk mounted trees)
#
# Only the mount point itself is touched when the target PUID/PGID cannot write
# it; existing files inside bind mounts are left alone. New files are created
# by the app (running as PUID/PGID) with umask 0022. Config/secrets/DB files are
# fixed only when they are not readable+writable by the target user.
# ---------------------------------------------------------------------------
mkdir -p "$CONFIG_DIR" "$SECRETS_DIR" "$DATABASE_DIRECTORY" "$RECORDS_DIR"

ensure_dir_writable() {
    dir=$1
    fix_mode=$2
    if [ -d "$dir" ] && "$PRIV" "$PUID:$PGID" test -w "$dir" 2>/dev/null; then
        return
    fi
    log "adjusting ownership of mount point $dir (top level only)"
    chown "$PUID:$PGID" "$dir" 2>/dev/null || log "WARN: cannot chown $dir"
    if [ -n "$fix_mode" ]; then
        chmod "$fix_mode" "$dir" 2>/dev/null || true
    fi
}

ensure_file_rw() {
    file=$1
    fix_mode=$2
    [ -f "$file" ] || return 0
    if "$PRIV" "$PUID:$PGID" test -r "$file" 2>/dev/null &&
       "$PRIV" "$PUID:$PGID" test -w "$file" 2>/dev/null; then
        return 0
    fi
    log "adjusting permissions of $file only"
    chown "$PUID:$PGID" "$file" 2>/dev/null || log "WARN: cannot chown $file"
    if [ -n "$fix_mode" ]; then
        chmod "$fix_mode" "$file" 2>/dev/null || log "WARN: cannot chmod $file"
    fi
}

ensure_dir_writable "$CONFIG_DIR" 0755
ensure_dir_writable "$SECRETS_DIR" 0700
ensure_dir_writable "$DATABASE_DIRECTORY" 0755
# Existing recordings are never recursively chowned; only the mount point.
ensure_dir_writable "$RECORDS_DIR" 0755

# Exact config/secrets files (atomic writes create new PUID-owned files later).
ensure_file_rw "$CONFIG_FILE" 0644
ensure_file_rw "$SECRETS_FILE" 0600

# bbolt files are flat and needed for startup; fix only those exact files.
for db_file in "$DATABASE_DIRECTORY"/*.db; do
    [ -f "$db_file" ] || continue
    ensure_file_rw "$db_file" ""
done

if [ "${BILIREC_ENTRYPOINT_DRY_RUN:-0}" = 1 ]; then
    log "dry run: uid/gid + dirs ready, not starting any process"
    exit 0
fi

# ---------------------------------------------------------------------------
# Start the requested mode
# ---------------------------------------------------------------------------
start_backend() {
    if [ "$#" -eq 0 ]; then
        set -- /usr/local/bin/bilirec
    fi
    log "starting '$*' as ${PUID}:${PGID} (dropping privileges via $PRIV)"
    exec "$PRIV" "$PUID:$PGID" "$@"
}

start_combined() {
    BILIREC_BIN=${BILIREC_BIN:-/usr/local/bin/bilirec}
    command -v nginx >/dev/null 2>&1 || die "combined mode requires nginx in the image"

    # nginx stays root (needs to bind :80 and read /app/web); bilirec is dropped.
    nginx -g 'daemon off;' &
    NGINX_PID=$!
    "$PRIV" "$PUID:$PGID" "$BILIREC_BIN" &
    APP_PID=$!

    STOPPING=0
    stop_children() {
        STOPPING=1
        kill -TERM "$APP_PID" "$NGINX_PID" 2>/dev/null || true
    }
    trap stop_children TERM INT

    STATUS=0
    while :; do
        if ! kill -0 "$APP_PID" 2>/dev/null; then
            wait "$APP_PID" 2>/dev/null || STATUS=$?
            break
        fi
        if ! kill -0 "$NGINX_PID" 2>/dev/null; then
            wait "$NGINX_PID" 2>/dev/null || STATUS=$?
            break
        fi
        sleep 1
    done

    stop_children
    [ "$STOPPING" = 1 ] && STATUS=0
    wait "$APP_PID" 2>/dev/null || true
    wait "$NGINX_PID" 2>/dev/null || true
    log "combined mode: process exited with status $STATUS"
    exit "$STATUS"
}

case "$MODE" in
    backend) start_backend "$@" ;;
    combined) start_combined ;;
    *)
        die "unknown entrypoint mode '$MODE' (expected 'backend' or 'combined')"
        ;;
esac
