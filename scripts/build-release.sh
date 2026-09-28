#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE="$ROOT/release"

rm -rf "$RELEASE"
mkdir -p "$RELEASE"

go build -trimpath -ldflags '-s -w' -o "$RELEASE/nodren" "$ROOT/Backend/Controller-Go"
cargo build --release --bin nodren-worker --manifest-path "$ROOT/Backend/Worker-Rust/Cargo.toml"

cp "$ROOT/Backend/Worker-Rust/target/release/nodren-worker" "$RELEASE/nodren-worker"
CORE="$(find "$ROOT/Backend/Worker-Rust/target/release/build" -type f -name 'libnodren_core.so' -print -quit)"
if [[ -z "$CORE" ]]; then
    echo "Native Core release library was not produced" >&2
    exit 1
fi
cp "$CORE" "$RELEASE/libnodren_core.so"

UI_PUBLISH="$RELEASE/.ui-publish"
dotnet publish "$ROOT/Frontend/Desktop/App/App.csproj" \
    --configuration Release \
    --runtime linux-x64 \
    --self-contained true \
    -p:PublishSingleFile=true \
    -p:IncludeNativeLibrariesForSelfExtract=true \
    -p:PublishTrimmed=false \
    --output "$UI_PUBLISH"
if [[ ! -f "$UI_PUBLISH/nodren-ui" ]]; then
    echo "Desktop UI release binary was not produced" >&2
    exit 1
fi
find "$UI_PUBLISH" -maxdepth 1 -type f ! -name '*.pdb' ! -name '*.xml' -exec cp {} "$RELEASE" \;
rm -rf "$UI_PUBLISH"

for artifact in nodren nodren-worker nodren-ui libnodren_core.so; do
    if [[ ! -f "$RELEASE/$artifact" ]]; then
        echo "Required release artifact is missing: $artifact" >&2
        exit 1
    fi
done

find "$RELEASE" -maxdepth 1 -type f -printf '%f %s bytes\n'
