# Shared helpers, sourced by the build scripts. POSIX sh.
ROOT=$(cd "$(dirname "$0")/.." && pwd)
DIST=${DIST:-$ROOT/dist}
VERSION=${VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)}
LDFLAGS_BASE="-s -w -X main.version=$VERSION"

die() { echo "error: $*" >&2; exit 1; }

# build_one GOOS GOARCH OUTDIR NAME PKG [gui]
build_one() {
	_os=$1 _arch=$2 _out=$3 _name=$4 _pkg=$5 _gui=${6:-}
	_ext=""; [ "$_os" = windows ] && _ext=.exe
	_ld=$LDFLAGS_BASE
	[ "$_os" = windows ] && [ -n "$_gui" ] && _ld="$_ld -H=windowsgui"
	mkdir -p "$_out"
	echo "  $_name ($_os/$_arch)"
	(cd "$ROOT" && CGO_ENABLED=0 GOOS=$_os GOARCH=$_arch \
		go build -trimpath -buildvcs=false -ldflags "$_ld" -o "$_out/$_name$_ext" "$_pkg") || die "build of $_name for $_os/$_arch failed"
}

# Fixed time stamp for reproducible archives (touch -t format, interpreted in UTC).
STAMP=202001010000.00

# gnu_tar prints the name of a GNU tar (tar, or gtar on macOS/BSD), or fails.
gnu_tar() {
	for _t in tar gtar; do
		if command -v "$_t" >/dev/null 2>&1 && "$_t" --version 2>/dev/null | grep -q 'GNU tar'; then
			echo "$_t"; return 0
		fi
	done
	return 1
}

# make_targz DIST_SUBDIR ARCHIVE : reproducible .tar.gz of DIST/DIST_SUBDIR (sorted, fixed owner and mtime).
make_targz() {
	_dir=$1 _arc=$2
	_tar=$(gnu_tar) || die "GNU tar is required for reproducible archives (the 'tar' here is not GNU tar; on macOS: brew install gnu-tar)"
	rm -f "$DIST/$_arc" "$DIST/${_arc%.gz}"
	# Written to a file first, so a failing tar or gzip cannot hide behind a pipe.
	(cd "$DIST" && "$_tar" --sort=name --owner=0 --group=0 --numeric-owner --mtime='2020-01-01 00:00Z' \
		-cf "${_arc%.gz}" "$_dir") || die "tar failed"
	(cd "$DIST" && gzip -n -f "${_arc%.gz}") || die "gzip failed"
	[ -s "$DIST/$_arc" ] || die "$_arc is empty"
}

# make_zip DIST_SUBDIR ARCHIVE : reproducible .zip (sorted names, fixed mtimes, no extra attributes: zip -X).
make_zip() {
	_dir=$1 _arc=$2
	command -v zip >/dev/null 2>&1 || die "zip not found (needed for the Windows archive)"
	rm -f "$DIST/$_arc"
	(cd "$DIST" && find "$_dir" -exec env TZ=UTC touch -t "$STAMP" {} +) || die "cannot set file times"
	(cd "$DIST" && find "$_dir" | LC_ALL=C sort | TZ=UTC zip -X -q "$_arc" -@) || die "zip failed"
	[ -s "$DIST/$_arc" ] || die "$_arc is empty"
}

# build_desktop GOOS GOARCH
build_desktop() {
	_os=$1 _arch=$2
	_out=$DIST/$_os-$_arch
	echo "== $_os/$_arch (version $VERSION)"
	rm -rf "$_out"
	build_one "$_os" "$_arch" "$_out" vide-run ./cmd/vide-run
	build_one "$_os" "$_arch" "$_out" vide-client ./cmd/vide-client gui
	build_one "$_os" "$_arch" "$_out" vide-studio ./cmd/vide-studio gui
	_base=vide2d-$VERSION-$_os-$_arch
	if [ "$_os" = windows ]; then
		make_zip "$_os-$_arch" "$_base.zip"
		echo "  -> $DIST/$_base.zip"
	else
		make_targz "$_os-$_arch" "$_base.tar.gz"
		echo "  -> $DIST/$_base.tar.gz"
	fi
}
