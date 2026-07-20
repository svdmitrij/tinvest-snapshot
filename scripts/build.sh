#!/usr/bin/env bash
# Cross-compile static binaries for Linux and Windows (amd64).
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p dist

# Detached worktrees do not always provide VCS metadata. Build output must be
# reproducible and independent of that metadata; use project storage instead
# of the quota-limited system tmpfs for compiler intermediates.
build_tmp="$(mktemp -d "$PWD/.go-build.XXXXXX")"
trap 'rm -rf "$build_tmp"' EXIT
export GOTMPDIR="$build_tmp"

CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -buildvcs=false -trimpath -o dist/tinvest-snapshot-linux-amd64   ./cmd/snapshot
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -trimpath -o dist/tinvest-snapshot-windows-amd64.exe ./cmd/snapshot

echo "Built:"
ls -la dist
