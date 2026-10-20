#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Entrypoint for the all-in-one image: start nginx (frontend) and bilirec
# (backend) together, forward SIGTERM/SIGINT, and stop the container when
# either process exits unexpectedly.
set -euo pipefail

nginx -g 'daemon off;' &
NGINX_PID=$!

bilirec &
APP_PID=$!

terminate() {
    kill -TERM "$APP_PID" "$NGINX_PID" 2>/dev/null || true
}
trap terminate TERM INT

# Exit as soon as either process dies; propagate a non-zero status so the
# container restart policy can react.
status=0
wait -n "$APP_PID" "$NGINX_PID" || status=$?
terminate
wait || true
exit "$status"
