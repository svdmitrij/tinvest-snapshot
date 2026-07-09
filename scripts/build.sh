#!/usr/bin/env bash
# Cross-compile static binaries for Linux and Windows (amd64).
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p dist

CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -o dist/tinvest-snapshot-linux-amd64   ./cmd/snapshot
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o dist/tinvest-snapshot-windows-amd64.exe ./cmd/snapshot

echo "Built:"
ls -la dist
