#!/bin/sh
# Builds the whole panel: interface first (it is embedded), then the binary.
#
#   ./scripts/build.sh            → bin/panel
#   VERSION=1.2.3 ./scripts/build.sh
set -eu

cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(cat VERSION 2>/dev/null || echo dev)}"

if [ ! -d web/node_modules ]; then
    echo "installing interface dependencies"
    npm --prefix web install --no-fund --no-audit
fi

echo "building the interface"
npm --prefix web run build

echo "building the panel"
mkdir -p bin
CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o bin/panel ./cmd/panel

echo
ls -lh bin/panel
echo "version: $(./bin/panel version)"
