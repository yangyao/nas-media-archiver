#!/bin/sh
# QTS /bin/sh service entry point. No nohup, systemd, or start-stop-daemon needed.
set -eu
PATH="/opt/bin:/opt/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export PATH
umask 077

PACKAGE_NAME="NASMediaArchiver"
REGISTRY="/etc/config/qpkg.conf"
PID_FILE="/var/run/nas-media-archiver.pid"
LOCK_DIR="/var/lock/nas-media-archiver.lock"
PACKAGE_DIR=$(/sbin/getcfg "$PACKAGE_NAME" Install_Path -f "$REGISTRY")
if [ -z "$PACKAGE_DIR" ] || [ ! -d "$PACKAGE_DIR" ]; then
    echo "$PACKAGE_NAME is not registered in App Center." >&2
    exit 1
fi
PACKAGE_DIR=$(readlink -f "$PACKAGE_DIR")
VOLUME_DIR=$(dirname "$(dirname "$PACKAGE_DIR")")
DATA_DIR="$VOLUME_DIR/.nas-media-archiver"
CONFIG_FILE="$DATA_DIR/service.conf"
TOKEN_FILE="$DATA_DIR/token"
LOG_FILE="$DATA_DIR/service.log"
BINARY="$PACKAGE_DIR/bin/archive"

LISTEN="0.0.0.0:8787"
NAME="My Archive Server"
ARCHIVE_BASE="/share/archive"
ROOTS="/share/ssd/upload,/share/archive"
STOP_TIMEOUT="120"
if [ -f "$CONFIG_FILE" ]; then
    # Administrator-owned configuration only; no user-supplied shell fragments.
    . "$CONFIG_FILE"
fi
case "$STOP_TIMEOUT" in ''|*[!0-9]*) echo "Invalid STOP_TIMEOUT." >&2; exit 1 ;; esac

running_pid() {
    [ -f "$PID_FILE" ] || return 1
    SERVICE_PID=$(cat "$PID_FILE")
    case "$SERVICE_PID" in ''|*[!0-9]*) return 1 ;; esac
    [ "$SERVICE_PID" -gt 1 ] || return 1
    kill -0 "$SERVICE_PID" 2>/dev/null || return 1
    # Do not signal an unrelated process if a stale PID has been reused.
    SERVICE_EXE=$(readlink "/proc/$SERVICE_PID/exe" 2>/dev/null) || return 1
    [ "$SERVICE_EXE" = "$BINARY" ]
}

acquire_lock() {
    if ! mkdir "$LOCK_DIR" 2>/dev/null; then
        echo "Another $PACKAGE_NAME lifecycle operation is in progress." >&2
        exit 1
    fi
    trap 'rmdir "$LOCK_DIR" 2>/dev/null || true' 0
    trap 'exit 1' 1 2 3 15
}

start_service() {
    ENABLED=$(/sbin/getcfg "$PACKAGE_NAME" Enable -u -d FALSE -f "$REGISTRY")
    if [ "$ENABLED" != "TRUE" ]; then
        echo "$PACKAGE_NAME is disabled in App Center." >&2
        return 1
    fi
    if running_pid; then
        echo "$PACKAGE_NAME is already running (PID $SERVICE_PID)."
        return 0
    fi
    [ -x "$BINARY" ] || { echo "Missing executable: $BINARY" >&2; return 1; }
    [ -f "$CONFIG_FILE" ] || { echo "Missing configuration: $CONFIG_FILE" >&2; return 1; }
    [ -d "$ARCHIVE_BASE" ] || { echo "Configure an existing archive directory first." >&2; return 1; }
    mkdir -p "$DATA_DIR/state"
    chmod 700 "$DATA_DIR" "$DATA_DIR/state"
    if [ -f "$TOKEN_FILE" ]; then chmod 600 "$TOKEN_FILE"; fi
    if [ -f "$LOG_FILE" ] && [ "$(wc -c < "$LOG_FILE")" -gt 5242880 ]; then
        mv -f "$LOG_FILE" "$LOG_FILE.1"
    fi
    rm -f "$PID_FILE"
    (
        # Ignore SIGHUP before exec so QTS/SSH parent exit does not stop a job.
        trap '' 1
        exec "$BINARY" serve \
            --listen "$LISTEN" \
            --state-dir "$DATA_DIR/state" \
            --archive-base "$ARCHIVE_BASE" \
            --roots "$ROOTS" \
            --token-file "$TOKEN_FILE" \
            --name "$NAME" >> "$LOG_FILE" 2>&1 < /dev/null
    ) &
    SERVICE_PID=$!
    printf '%s\n' "$SERVICE_PID" > "$PID_FILE"
    START_TRIES=0
    while [ "$START_TRIES" -lt 15 ]; do
        if ! kill -0 "$SERVICE_PID" 2>/dev/null; then
            rm -f "$PID_FILE"
            echo "$PACKAGE_NAME exited during startup; inspect $LOG_FILE." >&2
            return 1
        fi
        # The service generates its random token before accepting requests.
        # Health checks never pass a token through command arguments or logs.
        PORT=${LISTEN##*:}
        if command -v curl >/dev/null 2>&1; then
            if running_pid && curl --fail --silent --connect-timeout 1 --max-time 1 \
                "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then
                echo "$PACKAGE_NAME started (PID $SERVICE_PID)."
                return 0
            fi
        elif running_pid && [ -s "$TOKEN_FILE" ]; then
            echo "$PACKAGE_NAME started (PID $SERVICE_PID; curl unavailable, health unverified)."
            return 0
        fi
        START_TRIES=$((START_TRIES + 1))
        sleep 1
    done
    echo "$PACKAGE_NAME did not pass its startup health check; inspect $LOG_FILE." >&2
    stop_service || true
    return 1
}

stop_service() {
    if ! running_pid; then
        rm -f "$PID_FILE"
        echo "$PACKAGE_NAME is stopped."
        return 0
    fi
    kill -TERM "$SERVICE_PID"
    STOP_WAITED=0
    while running_pid; do
        if [ "$STOP_WAITED" -ge "$STOP_TIMEOUT" ]; then
            echo "$PACKAGE_NAME is still finishing a file; stop was not forced." >&2
            return 1
        fi
        STOP_WAITED=$((STOP_WAITED + 1))
        sleep 1
    done
    rm -f "$PID_FILE"
    echo "$PACKAGE_NAME stopped."
}

case "${1:-}" in
    start) acquire_lock; start_service ;;
    stop|remove) acquire_lock; stop_service ;;
    restart) acquire_lock; stop_service; start_service ;;
    status)
        if running_pid; then
            echo "$PACKAGE_NAME is running (PID $SERVICE_PID)."
        else
            echo "$PACKAGE_NAME is stopped."
            exit 3
        fi
        ;;
    *) echo "Usage: $0 {start|stop|restart|status|remove}" >&2; exit 2 ;;
esac
