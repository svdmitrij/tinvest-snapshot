#!/usr/bin/env bash
# Build native GUI artifacts. Linux needs X11/OpenGL development headers;
# Windows cross-build needs x86_64-w64-mingw32-gcc.
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p dist

CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath \
	-o dist/tinvest-gui-linux-amd64 ./cmd/gui

CC="${MINGW_CC:-x86_64-w64-mingw32-gcc}" \
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -trimpath \
	-ldflags '-H=windowsgui' -o dist/tinvest-gui-windows-amd64.exe ./cmd/gui

file dist/tinvest-gui-linux-amd64 dist/tinvest-gui-windows-amd64.exe
