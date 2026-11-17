# ViDe2D Android app

A thin Gradle project that hosts the Ebitengine view from `internal/mobile`
(the client shell running games in-process). The Go side is built into
`app/libs/videlib.aar` by `build/android.sh`; this folder holds only the wrapper.

Build (from the repo root): `build/android.sh` (prerequisites: docs/BUILD.md).
Result: `build/android/app/build/outputs/apk/debug/app-debug.apk`.

Gradle wrapper: not committed. Run `gradle wrapper --gradle-version 8.9` once in
this folder (needs Gradle installed), or open the folder in Android Studio.

Install: `adb install -r app/build/outputs/apk/debug/app-debug.apk`.
Put games in the app's private folder:
`adb push mygame /data/local/tmp/mygame && adb shell run-as com.vide2d.client cp -r /data/local/tmp/mygame files/games/`
(works for debug builds). Inside a game, hold three fingers for one second to
return to the library.

Status: written without an Android toolchain; the AAR class names
(`com.vide2d.client.mobile.EbitenView` / `Mobile`) follow ebitenmobile's naming
and must be checked on the first real build.
