#!/bin/sh
# Builds everything that can be built on this machine.
# Usage: build/all.sh [-desktop] [-android]   (no flags: both)
# Version is stamped from `git describe` (override with VERSION=...).
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
D=0; A=0
for f in "$@"; do
	case $f in
	-desktop) D=1 ;;
	-android) A=1 ;;
	-h|--help) sed -n '2,4p' "$0"; exit 0 ;;
	*) echo "unknown flag $f (use -desktop and/or -android)" >&2; exit 2 ;;
	esac
done
[ $D = 0 ] && [ $A = 0 ] && D=1 && A=1
if [ $D = 1 ]; then
	"$HERE/linux.sh"
	"$HERE/windows.sh"
fi
if [ $A = 1 ]; then
	# Android needs the SDK, NDK and ebitenmobile; when absent, say so and go on.
	if "$HERE/android.sh"; then :; else
		rc=$?
		[ $rc = 2 ] && echo "Android skipped (prerequisites missing, see above)." >&2 || exit $rc
	fi
fi
