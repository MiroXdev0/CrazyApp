#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET="${1:-linux-x64}"
if [[ "$TARGET" != "linux-x64" ]]; then
    echo "Usage: $0 linux-x64" >&2
    exit 2
fi

if [[ "$(uname -s)" != "Linux" || "$(uname -m)" != "x86_64" ]]; then
    echo "Linux x64 release must be built on Linux x86_64 or an equivalent Linux cross-build host" >&2
    exit 2
fi

RELEASE="$ROOT/release/LinuxX64"
rm -rf "$RELEASE"
mkdir -p "$RELEASE"

echo "[1/7] Building and testing Linux native Core"
bash "$ROOT/Core/build_core.sh"
cp "$ROOT/Core/build/libnodren_core.so" "$RELEASE/libnodren_core.so"

echo "[2/7] Building Linux Rust worker"
RUST_TARGET="x86_64-unknown-linux-gnu"
if command -v rustup >/dev/null 2>&1; then
    rustup target add "$RUST_TARGET"
fi
cargo build --release --target "$RUST_TARGET" --bin nodren-worker \
    --manifest-path "$ROOT/Backend/Worker-Rust/Cargo.toml"
cp "$ROOT/Backend/Worker-Rust/target/$RUST_TARGET/release/nodren-worker" "$RELEASE/nodren-worker"

echo "[3/7] Building Linux Go Controller"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -ldflags '-s -w' -o "$RELEASE/nodren" "$ROOT/Backend/Controller-Go"

echo "[4/7] Publishing Linux Avalonia UI"
UI_PUBLISH="$ROOT/.artifacts/nodren-app-linux-x64"
rm -rf "$UI_PUBLISH"
dotnet publish "$ROOT/Frontend/Desktop/App/App.csproj" \
    --configuration Release \
    --runtime linux-x64 \
    --self-contained true \
    -p:PublishSingleFile=true \
    -p:IncludeNativeLibrariesForSelfExtract=true \
    -p:PublishTrimmed=false \
    --output "$UI_PUBLISH"
find "$UI_PUBLISH" -maxdepth 1 -type f ! -name '*.pdb' ! -name '*.xml' -exec cp {} "$RELEASE" \;
rm -rf "$UI_PUBLISH"

echo "[5/7] Validating Linux release binaries"
for artifact in nodren nodren-worker NodrenApp libnodren_core.so; do
    if [[ ! -f "$RELEASE/$artifact" ]]; then
        echo "Missing release artifact: $artifact" >&2
        exit 1
    fi
done
if find "$RELEASE" -maxdepth 1 -type f \( -name '*.exe' -o -name '*.dll' \) | grep -q .; then
    echo "Windows artifact found in Linux release" >&2
    exit 1
fi
file "$RELEASE/nodren" "$RELEASE/nodren-worker" "$RELEASE/NodrenApp" "$RELEASE/libnodren_core.so"
for binary in nodren nodren-worker NodrenApp; do
    if ! file "$RELEASE/$binary" | grep -q 'ELF 64-bit.*x86-64'; then
        echo "Not an x86-64 ELF binary: $RELEASE/$binary" >&2
        exit 1
    fi
done
if ! file "$RELEASE/libnodren_core.so" | grep -q 'ELF 64-bit.*x86-64'; then
    echo "Native Core is not an x86-64 ELF shared library" >&2
    exit 1
fi
for symbol in nodren_core_create nodren_core_initialize nodren_core_destroy nodren_core_execute_workload; do
    nm -D --defined-only "$RELEASE/libnodren_core.so" | awk '{print $3}' | grep -Fxq "$symbol" || {
        echo "Required native symbol is missing: $symbol" >&2
        exit 1
    }
done

echo "[6/7] Verifying worker finds the adjacent native Core"
chmod +x "$RELEASE/nodren" "$RELEASE/nodren-worker" "$RELEASE/NodrenApp"
set +e
timeout 3s "$RELEASE/nodren-worker" --controller 127.0.0.1:1 >"$RELEASE/worker-load-test.out" 2>&1
worker_status=$?
set -e
if [[ $worker_status -ne 124 ]] || grep -Eiq 'native core library not found|failed to load native core|native core symbol missing' "$RELEASE/worker-load-test.out"; then
    cat "$RELEASE/worker-load-test.out" >&2
    echo "Worker did not load adjacent libnodren_core.so" >&2
    exit 1
fi
rm -f "$RELEASE/worker-load-test.out"

echo "[7/7] LinuxX64 release contents"
find "$RELEASE" -maxdepth 1 -type f -printf '%f %s bytes\n' | sort
echo "Linux x64 release created at $RELEASE"
