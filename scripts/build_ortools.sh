#!/usr/bin/env bash
# Downloads the prebuilt OR-Tools C++ bundle (shared library) and prepares the
# headers + libs so the Go OR-Tools solver (build tag `ortools`) can link
# dynamically against libortools.so.
#
# OR-Tools ships the solver only as a shared library in this bundle; the static
# .a files are only for transitive deps (absl/protobuf/...), so we link
# dynamically and bundle libortools.so next to the final binary at runtime.
#
# Usage:  scripts/build_ortools.sh
# After this, build with:  go build -tags ortools ./...
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OR_DIR="$ROOT/third_party/ortools"
BIND_DIR="$ROOT/third_party/ortools_bind"

# OR-Tools release tag + asset patch version (the download URL uses the tag,
# but the file name carries the full patch version).
OR_TAG="9.11"
OR_ASSET="9.11.4210"
OR_URL="https://github.com/google/or-tools/releases/download/v${OR_TAG}/or-tools_amd64_ubuntu-22.04_cpp_v${OR_ASSET}.tar.gz"

mkdir -p "$ROOT/third_party" "$OR_DIR"
if [ ! -d "$OR_DIR/include" ]; then
  echo ">> Downloading OR-Tools ${OR_TAG} (${OR_ASSET}) C++ ..."
  tmp="$(mktemp -d)"
  curl -fL "$OR_URL" -o "$tmp/ortools.tar.gz"
  tar -xzf "$tmp/ortools.tar.gz" -C "$tmp"
  src="$(ls -d "$tmp"/or-tools_*)"
  cp -r "$src"/. "$OR_DIR"/
  rm -rf "$tmp"
fi

echo ">> OR-Tools ready at $OR_DIR (link with -lortools; bundle libortools.so at runtime)."

echo ">> Compiling C++ binding (bind.cpp) ..."
CXX=${CXX:-g++}
"$CXX" -std=c++17 -O2 \
  -I"$OR_DIR/include" -I"$BIND_DIR" \
  -c "$BIND_DIR/bind.cpp" -o "$BIND_DIR/bind.o"

echo ">> Merging static dependency archives (absl/protobuf/...) into libortools_deps.a ..."
DEPS_OBJ="$BIND_DIR/_deps_objs"
rm -rf "$DEPS_OBJ" && mkdir -p "$DEPS_OBJ"
for a in "$OR_DIR/lib"/*.a; do
  d="$DEPS_OBJ/$(basename "$a" .a)"
  mkdir -p "$d"
  ( cd "$d" && ar x "$a" ) || true
done
find "$DEPS_OBJ" -name '*.o' -print0 | xargs -0 ar rcs "$OR_DIR/lib/libortools_deps.a"
rm -rf "$DEPS_OBJ"

echo ">> Done. libortools.so + bind.o + libortools_deps.a built."
echo "   Build with:  go build -tags ortools ./..."
