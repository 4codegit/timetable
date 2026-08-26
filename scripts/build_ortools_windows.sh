#!/usr/bin/env bash
# Downloads the prebuilt OR-Tools C++ bundle for Windows (MSVC / Visual Studio 2022)
# and compiles our C++ binding (bind.cpp) into bind_win.obj so the Go OR-Tools solver
# (build tag `ortools`) can link against ortools.dll on Windows.
#
# Usage (Windows runner, after VS + LLVM/clang-cl are available):
#   bash scripts/build_ortools_windows.sh
# After this, build with:  go build -tags ortools ./...

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WIN_DIR="$ROOT/third_party/ortools_win"
BIND_DIR="$ROOT/third_party/ortools_bind"

# OR-Tools release tag + asset (VS2022 C++ build => ortools.lib + ortools.dll).
OR_URL="https://github.com/google/or-tools/releases/download/v9.11/or-tools_x64_VisualStudio2022_cpp_v9.11.4210.zip"

mkdir -p "$ROOT/third_party" "$WIN_DIR"

if [ ! -d "$WIN_DIR/include" ]; then
  echo ">> Downloading OR-Tools 9.11 (VS2022) C++ ..."
  tmp="$(mktemp -d)"
  curl -fL "$OR_URL" -o "$tmp/ortools.zip"

  win_tmp="$(cygpath -w "$tmp")"
  powershell -NoProfile -Command "Expand-Archive -Path '$win_tmp\\ortools.zip' -DestinationPath '$win_tmp\\extract'"

  # Locate the extracted root that contains include/ lib/ bin/
  src="$(find "$tmp/extract" -maxdepth 2 -type d -name include | head -1 | xargs dirname)"
  cp -r "$src"/. "$WIN_DIR"/
  rm -rf "$tmp"
fi

echo ">> Compiling C++ binding (bind.cpp) with clang-cl ..."
export PATH="/c/Program Files/LLVM/bin:$PATH"
clang-cl -std=c++17 \
  -I"$WIN_DIR/include" -I"$BIND_DIR" \
  -c "$BIND_DIR/bind.cpp" -o "$BIND_DIR/bind_win.obj"

echo ">> Done. ortools.lib + ortools.dll + bind_win.obj ready under third_party/ortools_win."
