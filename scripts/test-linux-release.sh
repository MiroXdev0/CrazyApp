#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE="$ROOT/release/LinuxX64"
if [[ "$(uname -s)" != "Linux" || "$(uname -m)" != "x86_64" ]]; then
    echo "Linux release smoke test must run on Linux x86_64" >&2
    exit 2
fi
for artifact in nodren nodren-worker NodrenApp libnodren_core.so; do
    [[ -f "$RELEASE/$artifact" ]] || { echo "Missing $RELEASE/$artifact" >&2; exit 1; }
done

tcp_port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
http_port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
work_dir="$(mktemp -d)"
controller_pid=""
worker_pid=""
cleanup() {
    [[ -z "$worker_pid" ]] || kill "$worker_pid" 2>/dev/null || true
    [[ -z "$controller_pid" ]] || kill "$controller_pid" 2>/dev/null || true
    wait "$worker_pid" 2>/dev/null || true
    wait "$controller_pid" 2>/dev/null || true
    rm -rf "$work_dir"
}
trap cleanup EXIT

NODREN_AUTH_MODE=development \
NODREN_NODE_ADDR="127.0.0.1:$tcp_port" \
NODREN_HTTP_ADDR="127.0.0.1:$http_port" \
    "$RELEASE/nodren" >"$work_dir/controller.log" 2>&1 &
controller_pid=$!

python3 - "$http_port" <<'PY'
import json, sys, time, urllib.request
url = f"http://127.0.0.1:{sys.argv[1]}/health"
deadline = time.time() + 15
while time.time() < deadline:
    try:
        with urllib.request.urlopen(url, timeout=1) as response:
            if json.load(response)["status"] == "ok":
                raise SystemExit(0)
    except OSError:
        time.sleep(0.1)
raise SystemExit("Controller health check timed out")
PY

NODREN_AUTH_MODE=development \
NODREN_CONTROLLER_ADDR="127.0.0.1:$tcp_port" \
    "$RELEASE/nodren-worker" --id LINUX-RELEASE-WORKER --cpu-cores 2 --ram-gb 4 \
    >"$work_dir/worker.log" 2>&1 &
worker_pid=$!

python3 - "$http_port" <<'PY'
import json, sys, time, urllib.request
base = f"http://127.0.0.1:{sys.argv[1]}"
deadline = time.time() + 15
while time.time() < deadline:
    try:
        with urllib.request.urlopen(base + "/v1/nodes", timeout=1) as response:
            nodes = json.load(response)
            if nodes and nodes[0]["state"] == "READY":
                raise SystemExit(0)
    except OSError:
        time.sleep(0.1)
raise SystemExit("Worker registration timed out")
PY

NODREN_CONTROLLER_URL="http://127.0.0.1:$http_port" \
    "$RELEASE/NodrenApp" --self-test

NODREN_CONTROLLER_URL="http://127.0.0.1:$http_port" \
    "$RELEASE/nodren" run sum 1 2 3 2>&1 | grep -Fq 'Result       6'

if [[ -n "${DISPLAY:-}" || -n "${WAYLAND_DISPLAY:-}" ]]; then
    set +e
    timeout 5s "$RELEASE/NodrenApp" >"$work_dir/ui.log" 2>&1
    ui_status=$?
    set -e
    if [[ "$ui_status" -eq 127 ]]; then
        echo "UI launch timeout utility is unavailable" >&2
        exit 1
    fi
    if [[ "$ui_status" -ne 124 ]]; then
        echo "UI exited before the smoke-test timeout (status $ui_status)" >&2
        cat "$work_dir/ui.log" >&2
        exit 1
    fi
fi

echo "PASS: LinuxX64 Controller, worker, adjacent native Core loading, UI API self-test, and sum=6"
