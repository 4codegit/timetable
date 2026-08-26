#!/usr/bin/env bash
# Downloads prebuilt OR-Tools C++ and builds a combined static archive (libortools_full.a)
# so the Go OR-Tools solver (build tag `ortools`) can link without enumerating every dependency.
#
# Usage:  scripts/build_ortools.sh
# After this, build with:  go build -tags ortools ./...   (or wails build -tags ortools ...)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OR_DIR="$ROOT/third_party/ortools"
BIND_DIR="$ROOT/third_party/ortools_bind"
LIB="$OR_DIR/lib"

# Pick a version. Update OR_VERSION if you need a newer OR-Tools.
OR_VERSION="9.11"
# Prebuilt C++ bundle (Linux x86_64). For other platforms adjust the URL below.
OR_URL="https://github.com/google/or-tools/releases/download/v${OR_VERSION}/or-tools_amd64_ubuntu-22.04_cpp_v${OR_VERSION}.tar.gz"

mkdir -p "$ROOT/third_party"
if [ ! -d "$OR_DIR/include" ]; then
  echo ">> Downloading OR-Tools ${OR_VERSION} C++ ..."
  tmp="$(mktemp -d)"
  curl -fL "$OR_URL" -o "$tmp/ortools.tar.gz"
  tar -xzf "$tmp/ortools.tar.gz" -C "$tmp"
  mv "$tmp"/or-tools_* "$OR_DIR"
  rm -rf "$tmp"
fi

echo ">> Compiling C++ binding ..."
CXX=${CXX:-g++}
"$CXX" -std=c++17 -O2 \
  -I"$OR_DIR/include" -I"$BIND_DIR" \
  -c "$BIND_DIR/bind.cpp" -o "$BIND_DIR/bind.o"

echo ">> Merging OR-Tools + deps + binding into libortools_full.a ..."
mkdir -p "$LIB"
rm -rf "$BIND_DIR/_objs" && mkdir -p "$BIND_DIR/_objs"
cp "$BIND_DIR/bind.o" "$BIND_DIR/_objs/"

# Extract every .o from every .a shipped with OR-Tools, plus our binding object.
for a in "$LIB"/*.a; do
  [ -e "$a" ] || continue
  d="$BIND_DIR/_objs/$(basename "$a" .a)"
  mkdir -p "$d"
  ( cd "$d" && ar x "$a" )
done

# Re-pack everything into one fat archive.
ar rcs "$LIB/libortools_full.a" "$BIND_DIR"/_objs/*.o "$BIND_DIR"/_objs/*/*.o

echo ">> Done. libortools_full.a built at $LIB/libortools_full.a"
echo "   Now build with:  go build -tags ortools ./...   (or: wails build -platform windows/amd64 -tags ortools)"
