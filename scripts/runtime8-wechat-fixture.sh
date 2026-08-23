#!/usr/bin/env bash
# runtime8-wechat-fixture.sh — start/stop the isolated Runtime 8.0 WeChat fixture.
#
# Usage:
#   ./scripts/runtime8-wechat-fixture.sh start [PORT]
#   ./scripts/runtime8-wechat-fixture.sh stop
#   ./scripts/runtime8-wechat-fixture.sh status
#
# This script locates WeChat DevTools, opens the isolated fixture project,
# and enables the Automator endpoint on a dedicated port. It does NOT
# upload or alter the production Lacoeur project.
#
# The lifecycle follows the accepted WeChat DevTools CLI pattern:
#   1. cli open  — open the project in the IDE
#   2. cli auto  — enable the automation endpoint on the specified port
#   3. cli close — close the project (stop)
#   4. cli quit  — quit the IDE (stop)
#
# Environment:
#   ARGUS_WX_DEVTOOLS  — path to WeChat DevTools.app (optional, auto-detected)
#   ARGUS_WX_PORT      — automator port (default 9420)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"
FIXTURE_DIR="$REPO_DIR/fixtures/mp-weixin-runtime8"
LOG_FILE="/tmp/argus-runtime8-fixture.log"
STATE_FILE="/tmp/argus-runtime8-fixture.state"

PORT="${ARGUS_WX_PORT:-9420}"

# Locate WeChat DevTools CLI
find_devtools_cli() {
    local cli=""
    # Check common locations
    local paths=(
        "/Applications/wechatwebdevtools.app/Contents/MacOS/cli"
        "$HOME/Applications/wechatwebdevtools.app/Contents/MacOS/cli"
        "${ARGUS_WX_DEVTOOLS:-/Applications/wechatwebdevtools.app}/Contents/MacOS/cli"
    )
    for p in "${paths[@]}"; do
        if [ -x "$p" ]; then
            cli="$p"
            break
        fi
    done
    echo "$cli"
}

cmd_start() {
    local port="${1:-$PORT}"
    local cli
    cli=$(find_devtools_cli)
    if [ -z "$cli" ]; then
        echo "ERROR: WeChat DevTools CLI not found." >&2
        echo "Install WeChat DevTools or set ARGUS_WX_DEVTOOLS to the .app path." >&2
        echo "Download: https://developers.weixin.qq.com/miniprogram/dev/devtools/download.html" >&2
        exit 1
    fi

    if [ ! -d "$FIXTURE_DIR" ]; then
        echo "ERROR: Fixture directory not found: $FIXTURE_DIR" >&2
        exit 1
    fi

    echo "Starting Runtime 8.0 fixture on port $port..."
    echo "  DevTools CLI: $cli"
    echo "  Fixture: $FIXTURE_DIR"

    # Step 1: Open the fixture project in DevTools IDE.
    echo "Opening fixture project..."
    "$cli" open --project "$FIXTURE_DIR" >"$LOG_FILE" 2>&1 || {
        echo "ERROR: cli open failed. Check $LOG_FILE" >&2
        exit 1
    }

    # Step 2: Enable the automation endpoint on the specified port.
    # This is the accepted lifecycle: cli auto enables the WebSocket
    # automator endpoint separately from project opening.
    echo "Enabling automation on port $port..."
    "$cli" auto --project "$FIXTURE_DIR" --auto-port "$port" >>"$LOG_FILE" 2>&1 || {
        echo "ERROR: cli auto failed. Check $LOG_FILE" >&2
        exit 1
    }

    # Record state for stop/cleanup.
    echo "$port" > "$STATE_FILE"

    # Wait for the automator endpoint to be reachable.
    echo "Waiting for automator endpoint on port $port..."
    local retries=0
    while [ $retries -lt 30 ]; do
        if curl -s "http://127.0.0.1:$port" >/dev/null 2>&1; then
            echo "Automator endpoint is ready on port $port."
            echo "Set ARGUS_WX_LIVE=1 ARGUS_WX_PORT=$port to run tests."
            return 0
        fi
        sleep 1
        retries=$((retries + 1))
    done

    echo "WARNING: Automator endpoint not reachable after 30s." >&2
    echo "Check $LOG_FILE for details." >&2
    echo "The fixture may still be starting. Verify manually with:" >&2
    echo "  curl http://127.0.0.1:$port" >&2
    return 1
}

cmd_stop() {
    local cli
    cli=$(find_devtools_cli)

    # Close the fixture project (graceful).
    if [ -n "$cli" ] && [ -d "$FIXTURE_DIR" ]; then
        "$cli" close --project "$FIXTURE_DIR" 2>/dev/null || true
    fi

    # Quit the IDE if no other projects are open.
    if [ -n "$cli" ]; then
        "$cli" quit 2>/dev/null || true
    fi

    rm -f "$STATE_FILE"
    echo "Runtime 8.0 fixture stopped."
}

cmd_status() {
    local port="${1:-$PORT}"
    if curl -s "http://127.0.0.1:$port" >/dev/null 2>&1; then
        echo "Automator endpoint is running on port $port."
        return 0
    else
        echo "Automator endpoint is NOT running on port $port."
        return 1
    fi
}

case "${1:-}" in
    start) cmd_start "${2:-}" ;;
    stop) cmd_stop ;;
    status) cmd_status "${2:-}" ;;
    *)
        echo "Usage: $0 {start|stop|status} [PORT]"
        exit 1
        ;;
esac
