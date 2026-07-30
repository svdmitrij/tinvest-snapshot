#!/usr/bin/env bash
# Build native GUI artifacts.
#
#   ./scripts/build-gui.sh [linux|windows|all]   (default: all)
#
# Linux needs X11/OpenGL development headers (xorg-dev on Debian/Ubuntu).
# Windows cross-build needs x86_64-w64-mingw32-gcc; override with MINGW_CC.
set -euo pipefail

target="${1:-all}"
case "$target" in
linux | windows | all) ;;
*)
	echo "Использование: $0 [linux|windows|all]" >&2
	exit 2
	;;
esac

cd "$(dirname "$0")/.."
mkdir -p dist

built=()

if [[ "$target" == "linux" || "$target" == "all" ]]; then
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false \
		-o dist/tinvest-gui-linux-amd64 ./cmd/gui
	built+=(dist/tinvest-gui-linux-amd64)
fi

if [[ "$target" == "windows" || "$target" == "all" ]]; then
	cc="${MINGW_CC:-x86_64-w64-mingw32-gcc}"
	if ! command -v "$cc" >/dev/null 2>&1; then
		echo "Не найден кросс-компилятор $cc — нужен для сборки Windows." >&2
		echo "Установите mingw-w64 или соберите только Linux: $0 linux" >&2
		exit 3
	fi
	CC="$cc" CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -trimpath -buildvcs=false \
		-ldflags '-H=windowsgui' -o dist/tinvest-gui-windows-amd64.exe ./cmd/gui
	built+=(dist/tinvest-gui-windows-amd64.exe)
fi

file "${built[@]}"
