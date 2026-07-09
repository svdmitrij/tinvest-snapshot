#!/usr/bin/env bash
# Build release archives for Linux and Windows (amd64). Each archive contains
# the binary, config.example.json and README.md under a tinvest-snapshot/ dir.
set -euo pipefail

cd "$(dirname "$0")/.."
VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
OUT="dist"
rm -rf "$OUT"
mkdir -p "$OUT"

build() {
	local goos="$1" ext="$2" arcname="$3"
	local stage="$OUT/tinvest-snapshot"
	mkdir -p "$stage"
	CGO_ENABLED=0 GOOS="$goos" GOARCH=amd64 go build -trimpath \
		-o "$stage/tinvest-snapshot${ext}" ./cmd/snapshot
	cp config.example.json README.md "$stage/"
	( cd "$OUT" && "${@:4}" )
	rm -rf "$stage"
}

# Linux: tar.gz
build linux "" "tinvest-snapshot-${VERSION}-linux-amd64.tar.gz" \
	tar -czf "tinvest-snapshot-${VERSION}-linux-amd64.tar.gz" tinvest-snapshot

# Windows: zip if available, otherwise tar.gz
if command -v zip >/dev/null 2>&1; then
	build windows ".exe" "tinvest-snapshot-${VERSION}-windows-amd64.zip" \
		zip -qr "tinvest-snapshot-${VERSION}-windows-amd64.zip" tinvest-snapshot
else
	build windows ".exe" "tinvest-snapshot-${VERSION}-windows-amd64.tar.gz" \
		tar -czf "tinvest-snapshot-${VERSION}-windows-amd64.tar.gz" tinvest-snapshot
fi

( cd "$OUT" && sha256sum tinvest-snapshot-* > SHA256SUMS.txt )
echo "Release artifacts (version ${VERSION}):"
ls -la "$OUT"
