#!/bin/bash
# Build libghostty-vt as an iOS (device + simulator) xcframework from Ghostty's
# sources, at the commit GoRex's terminal binding uses, into
# ios/Frameworks/GhosttyVt.xcframework (gitignored). Needs Xcode; fetches the
# Zig that Ghostty asks for when ZIG is not set. Modelled on rootshell's
# scripts/build-framework.sh.
#
# The app links GhosttyKit too, a whole Ghostty with the same C libraries
# and Zig runtime inside: each library here is merged into one object that
# keeps only libghostty-vt's C API global, so the two link side by side;
# and the module map moves to a folder of its own, as Xcode copies both
# frameworks' headers into one folder.
#
# Usage: ios/scripts/build-ghostty-vt.sh [--ghostty-source <path>]
set -euo pipefail

# The commit of si-mods/mygo's plugins/terminal/internal/libbuild.
COMMIT=108cacb17ed8990840cd14abfce43d8ed8503ca1
ZIG_VERSION=0.17.0

IOS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CACHE="${XDG_CACHE_HOME:-$HOME/.cache}/gorex-ios"
SRC="$CACHE/ghostty"
[[ "${1:-}" == --ghostty-source ]] && SRC="$2"

if [[ -z "${ZIG:-}" ]]; then
    ZIG="$CACHE/zig-$ZIG_VERSION/zig"
    if [[ ! -x "$ZIG" ]]; then
        mkdir -p "$CACHE/zig-$ZIG_VERSION"
        curl -fL "https://ziglang.org/download/$ZIG_VERSION/zig-$(uname -m | sed s/arm64/aarch64/)-macos-$ZIG_VERSION.tar.xz" |
            tar -xJ --strip-components=1 -C "$CACHE/zig-$ZIG_VERSION"
    fi
fi

if [[ ! -d "$SRC/.git" ]]; then
    git clone --filter=blob:none https://github.com/ghostty-org/ghostty "$SRC"
fi
git -C "$SRC" fetch -q origin "$COMMIT" 2>/dev/null || true
git -C "$SRC" checkout -q --detach "$COMMIT"

echo "Ghostty $(git -C "$SRC" rev-parse --short HEAD), Zig $("$ZIG" version)"
PREFIX="$(mktemp -d)"
trap 'rm -rf "$PREFIX"' EXIT
(cd "$SRC" && "$ZIG" build -Demit-lib-vt -Demit-xcframework=true \
    -Doptimize=ReleaseFast -Dstrip=true --prefix "$PREFIX")

OUT="$IOS_DIR/Frameworks/GhosttyVt.xcframework"
rm -rf "$OUT"
mkdir -p "$IOS_DIR/Frameworks"
ditto "$PREFIX/lib/ghostty-vt.xcframework" "$OUT"
for lib in "$OUT"/*/*.a; do
    # The platform of the slice; the app's deployment target, or macOS 13.
    case "$(basename "$(dirname "$lib")")" in
    ios-*-simulator) platform=ios-simulator version=26.0 ;;
    ios-*) platform=ios version=26.0 ;;
    *) platform=macos version=13.0 ;;
    esac
    nm -gU "$lib" | awk '$3 ~ /^_ghostty_/ && $3 !~ /^_ghostty_(simd|hwy)_/ { print $3 }' | sort -u >"$PREFIX/api"
    parts=()
    for arch in $(lipo -archs "$lib"); do
        lipo "$lib" -thin "$arch" -output "$PREFIX/$arch.a" 2>/dev/null || cp "$lib" "$PREFIX/$arch.a"
        xcrun ld -r -arch "$arch" -platform_version "$platform" "$version" "$version" -all_load "$PREFIX/$arch.a" -exported_symbols_list "$PREFIX/api" -o "$PREFIX/$arch.o"
        rm -f "$PREFIX/$arch.a"
        xcrun libtool -static -o "$PREFIX/$arch.a" "$PREFIX/$arch.o" 2>/dev/null
        parts+=("$PREFIX/$arch.a")
    done
    lipo -create "${parts[@]}" -output "$lib"
    rm -f "${parts[@]}"
    headers="$(dirname "$lib")/Headers"
    mkdir -p "$headers/GhosttyVt"
    rm "$headers/module.modulemap"
    printf 'module GhosttyVt {\n    header "../ghostty/vt.h"\n    export *\n}\n' >"$headers/GhosttyVt/module.modulemap"
done
# Check the device library has what the app calls (a string, not a pipe:
# grep -q closing it early would fail nm under pipefail).
symbols="$(nm -g "$OUT"/ios-arm64/*.a 2>/dev/null)"
for sym in _ghostty_snapshot_decoder_new_buf _ghostty_formatter_terminal_new _ghostty_terminal_vt_write; do
    [[ "$symbols" == *"T $sym"* ]] || { echo "ERROR: $sym is missing from the iOS library" >&2; exit 1; }
done
echo "Built $OUT"
