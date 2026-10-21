#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
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
#   3. Create and chown the runtime dirs: /app/config, /app/secrets,
#      /app/database, /app/records. `records` is chowned recursively only when
#      its top-level owner differs (large recording trees stay cheap).
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
# Runtime dirs + ownership
# ---------------------------------------------------------------------------
mkdir -p "$CONFIG_DIR" "$SECRETS_DIR" "$DATABASE_DIRECTORY" "$RECORDS_DIR"
chmod 0755 "$CONFIG_DIR" "$DATABASE_DIRECTORY" "$RECORDS_DIR" 2>/dev/null || true
chmod 0700 "$SECRETS_DIR" 2>/dev/null || true

# config/secrets/database are small; make them fully writable by the app user.
chown -R "$PUID:$PGID" "$CONFIG_DIR" "$SECRETS_DIR" "$DATABASE_DIRECTORY"

# records can be huge: only recurse when the top-level owner/group is wrong.
records_owner=$(stat -c '%u:%g' "$RECORDS_DIR" 2>/dev/null || echo '')
if [ "$records_owner" != "$PUID:$PGID" ]; then
    log "chown -R $PUID:$PGID $RECORDS_DIR (top-level owner was ${records_owner:-unknown})"
    chown -R "$PUID:$PGID" "$RECORDS_DIR"
else
    chown "$PUID:$PGID" "$RECORDS_DIR"
fi
chown "$PUID:$PGID" "$APP_DIR" 2>/dev/null || true

# Enforce CONFIG-V2 modes on existing files (write paths enforce this too).
if [ -f "$CONFIG_FILE" ]; then
    chown "$PUID:$PGID" "$CONFIG_FILE" 2>/dev/null || log "WARN: cannot chown $CONFIG_FILE"
    chmod 0644 "$CONFIG_FILE" 2>/dev/null || log "WARN: cannot chmod $CONFIG_FILE to 0644"
fi
if [ -f "$SECRETS_FILE" ]; then
    chown "$PUID:$PGID" "$SECRETS_FILE" 2>/dev/null || log "WARN: cannot chown $SECRETS_FILE"
    chmod 0600 "$SECRETS_FILE" 2>/dev/null || log "WARN: cannot chmod $SECRETS_FILE to 0600"
fi

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
