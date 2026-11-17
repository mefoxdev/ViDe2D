#!/bin/sh
# Android: client + runtime -> build/android/app/libs/videlib.aar, then an APK via Gradle.
# Usage: build/android.sh [-aar-only]
# The studio is not built for Android.
set -eu
. "$(dirname "$0")/common.sh"
AARONLY=0; [ "${1:-}" = "-aar-only" ] && AARONLY=1

missing=""
need() { missing="$missing\n  - $1"; }
command -v go >/dev/null 2>&1 || need "Go (https://go.dev/dl/)"
command -v java >/dev/null 2>&1 || need "a JDK 17+ ('java' not in PATH; set JAVA_HOME)"
SDK=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}
[ -n "$SDK" ] && [ -d "$SDK" ] || need "Android SDK: set ANDROID_HOME (e.g. ~/Android/Sdk; install with Android Studio or cmdline-tools: 'sdkmanager \"platforms;android-34\" \"build-tools;34.0.0\"')"
NDK=${ANDROID_NDK_HOME:-}
if [ -z "$NDK" ] && [ -n "$SDK" ] && [ -d "$SDK/ndk" ]; then
	if [ -n "${NDK_VERSION:-}" ]; then
		# explicit choice, e.g. NDK_VERSION=27.0.12077973
		NDK=$SDK/ndk/$NDK_VERSION
	elif printf '1\n' | sort -V >/dev/null 2>&1; then
		# newest installed version (version order: 9.x < 27.x)
		NDK_LATEST=$(ls -1 "$SDK/ndk" | sort -V | tail -n 1)
		[ -z "$NDK_LATEST" ] || NDK=$SDK/ndk/$NDK_LATEST
	else
		need "a 'sort' with -V to pick the newest NDK: set NDK_VERSION (e.g. 27.0.12077973) or ANDROID_NDK_HOME"
	fi
fi
[ -n "$NDK" ] && [ -d "$NDK" ] || need "Android NDK (NDK_VERSION picks one under \$ANDROID_HOME/ndk, default the newest): install with 'sdkmanager \"ndk;27.0.12077973\"' or set ANDROID_NDK_HOME"
EBITENMOBILE=$(command -v ebitenmobile 2>/dev/null || true)
[ -z "$EBITENMOBILE" ] && [ -x "$(go env GOPATH 2>/dev/null)/bin/ebitenmobile" ] && EBITENMOBILE=$(go env GOPATH)/bin/ebitenmobile
[ -n "$EBITENMOBILE" ] || need "ebitenmobile: 'go install github.com/hajimehoshi/ebiten/v2/cmd/ebitenmobile@v2.10.0' (and make sure \$(go env GOPATH)/bin is in PATH)"
if [ -n "$missing" ]; then
	printf 'Cannot build for Android; missing:%b\n\nSee docs/BUILD.md, section Android.\n' "$missing" >&2
	exit 2
fi

export ANDROID_HOME=$SDK ANDROID_NDK_HOME=$NDK
OUT=$ROOT/build/android/app/libs
mkdir -p "$OUT"
echo "== Android AAR (version $VERSION)"
(cd "$ROOT" && "$EBITENMOBILE" bind -target android -androidapi 24 -javapkg com.vide2d.client \
	-trimpath -ldflags "$LDFLAGS_BASE" -o "$OUT/videlib.aar" ./internal/mobile) || die "ebitenmobile bind failed"
echo "  -> $OUT/videlib.aar"
[ $AARONLY = 1 ] && exit 0

if [ -x "$ROOT/build/android/gradlew" ]; then GRADLE=$ROOT/build/android/gradlew
elif command -v gradle >/dev/null 2>&1; then GRADLE=gradle
else
	echo "AAR is ready, but Gradle is not installed: open build/android in Android Studio, or install Gradle 8.x and run 'gradle wrapper' in build/android (see build/android/README.md)." >&2
	exit 2
fi
(cd "$ROOT/build/android" && "$GRADLE" -PvideVersion="$VERSION" assembleDebug) || die "gradle failed"
echo "  -> $ROOT/build/android/app/build/outputs/apk/debug/app-debug.apk"
