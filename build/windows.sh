#!/bin/sh
# Windows desktop cross-builds -> dist/windows-<arch>/ (+ .zip). Needs only Go and zip.
# Usage: build/windows.sh [amd64] [arm64]   (default: both)
set -eu
. "$(dirname "$0")/common.sh"
command -v go >/dev/null 2>&1 || die "Go is not installed (https://go.dev/dl/)"
[ $# -gt 0 ] || set -- amd64 arm64
for a in "$@"; do
	case $a in amd64|arm64) build_desktop windows "$a" ;; *) die "unknown arch $a" ;; esac
done
