#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd "$(dirname "$0")/../.." && pwd)
installer="$repo_root/scripts/install.sh"
temp_root=$(mktemp -d "${TMPDIR:-/tmp}/just-code-installer-test.XXXXXX")
trap 'rm -rf "$temp_root"' 0

fake_bin="$temp_root/fake bin"
mkdir -p "$fake_bin"
real_mv=$(command -v mv)

cat > "$fake_bin/uname" <<'EOF'
#!/bin/sh
case "$1" in
	-s) printf '%s\n' "${FAKE_OS:-Linux}" ;;
	-m) printf '%s\n' "${FAKE_ARCH:-x86_64}" ;;
	*) exit 2 ;;
esac
EOF

cat > "$fake_bin/curl" <<'EOF'
#!/bin/sh
set -eu
output=''
url=''
while [ "$#" -gt 0 ]; do
	case "$1" in
		-o|--output) output=$2; shift 2 ;;
		-w|--write-out) shift 2 ;;
		-*) shift ;;
		*) url=$1; shift ;;
	esac
done
printf '%s\n' "$url" >> "$FAKE_URL_LOG"
case "$url" in
	*/releases/latest)
		printf '%s' "$FAKE_RELEASE_URL"
		;;
	*/SHA256SUMS)
		cp "$FAKE_SUMS" "$output"
		;;
	*/"$FAKE_ASSET")
		if [ "${FAKE_FAIL_DOWNLOAD:-0}" = 1 ]; then
			printf 'partial' > "$output"
			exit 22
		fi
		cp "$FAKE_BINARY" "$output"
		;;
	*)
		printf 'unexpected URL: %s\n' "$url" >&2
		exit 22
		;;
esac
EOF

cat > "$fake_bin/mv" <<'EOF'
#!/bin/sh
set -eu
destination=''
for arg do destination=$arg; done
if [ "${FAKE_FAIL_REPLACE:-0}" = 1 ] && [ "$destination" = "$FAKE_TARGET" ]; then
	exit 1
fi
exec "$REAL_MV" "$@"
EOF
chmod +x "$fake_bin/uname" "$fake_bin/curl" "$fake_bin/mv"

fake_binary="$temp_root/fake just-code"
cat > "$fake_binary" <<'EOF'
#!/bin/sh
[ "${1:-}" = setup ] || exit 7
printf called > "$FAKE_SETUP_LOG"
exit "${FAKE_SETUP_EXIT:-0}"
EOF
asset='just-code-linux-x64'
release_tag='just-code-v9.8.7'
release_url="https://github.com/etalab-ia/just-code/releases/tag/$release_tag"
if command -v sha256sum >/dev/null 2>&1; then
	expected=$(sha256sum "$fake_binary" | awk '{print $1}')
else
	expected=$(shasum -a 256 "$fake_binary" | awk '{print $1}')
fi
good_sums="$temp_root/good SHA256SUMS"
printf '%064d  just-code-windows-x64.exe\n%s  just-code-darwin-arm64\n%s  just-code-linux-arm64\n%s  %s\n' \
	0 "$expected" "$expected" "$expected" "$asset" > "$good_sums"
bad_sums="$temp_root/bad SHA256SUMS"
printf '%064d  %s\n' 0 "$asset" > "$bad_sums"

run_installer() {
	home_dir=$1
	sums_file=$2
	fail_download=$3
	fail_replace=$4
	setup_exit=$5
	platform_os=${6:-Linux}
	platform_arch=${7:-x86_64}
	asset_name=${8:-$asset}
	FAKE_OS=$platform_os \
	FAKE_ARCH=$platform_arch \
	FAKE_ASSET=$asset_name \
	FAKE_BINARY="$fake_binary" \
	FAKE_SUMS="$sums_file" \
	FAKE_URL_LOG="$temp_root/urls" \
	FAKE_SETUP_LOG="$temp_root/setup-called" \
	FAKE_RELEASE_URL="$release_url" \
	FAKE_FAIL_DOWNLOAD=$fail_download \
	FAKE_FAIL_REPLACE=$fail_replace \
	FAKE_SETUP_EXIT=$setup_exit \
	FAKE_TARGET="$home_dir/.local/bin/just-code" \
	REAL_MV="$real_mv" \
	HOME="$home_dir" PATH="$fake_bin:$PATH" \
		"$installer"
}

home="$temp_root/home with spaces"
mkdir -p "$home/.local/bin"
printf 'old version\n' > "$home/.local/bin/just-code"
run_installer "$home" "$good_sums" 0 0 0 > "$temp_root/success.out"
cmp -s "$home/.local/bin/just-code" "$fake_binary" || { printf 'new binary was not installed\n' >&2; exit 1; }
[ "$(cat "$home/.local/bin/just-code.previous")" = 'old version' ] || {
	printf 'prior binary was not retained\n' >&2
	exit 1
}
[ -f "$temp_root/setup-called" ] || { printf 'setup was not invoked\n' >&2; exit 1; }
grep -F "$release_tag" "$temp_root/urls" >/dev/null || { printf 'downloads were not pinned to one release\n' >&2; exit 1; }

mac_home="$temp_root/mac home"
mkdir -p "$mac_home"
run_installer "$mac_home" "$good_sums" 0 0 0 Darwin arm64 just-code-darwin-arm64 > "$temp_root/mac.out"
cmp -s "$mac_home/.local/bin/just-code" "$fake_binary" || { printf 'macOS arm64 asset mapping failed\n' >&2; exit 1; }

arm_home="$temp_root/linux arm home"
mkdir -p "$arm_home"
run_installer "$arm_home" "$good_sums" 0 0 0 Linux aarch64 just-code-linux-arm64 > "$temp_root/arm.out"
cmp -s "$arm_home/.local/bin/just-code" "$fake_binary" || { printf 'Linux arm64 asset mapping failed\n' >&2; exit 1; }

target="$home/.local/bin/just-code"
printf 'keep me\n' > "$target"
cp "$target" "$temp_root/expected-target"
if run_installer "$home" "$bad_sums" 0 0 0 > "$temp_root/mismatch.out" 2>&1; then
	printf 'checksum mismatch unexpectedly succeeded\n' >&2
	exit 1
fi
cmp -s "$target" "$temp_root/expected-target" || { printf 'checksum failure changed the installed binary\n' >&2; exit 1; }

if run_installer "$home" "$good_sums" 1 0 0 > "$temp_root/download.out" 2>&1; then
	printf 'partial download unexpectedly succeeded\n' >&2
	exit 1
fi
cmp -s "$target" "$temp_root/expected-target" || { printf 'download failure changed the installed binary\n' >&2; exit 1; }

if run_installer "$home" "$good_sums" 0 1 0 > "$temp_root/replace.out" 2>&1; then
	printf 'interrupted replacement unexpectedly succeeded\n' >&2
	exit 1
fi
cmp -s "$target" "$temp_root/expected-target" || { printf 'failed replacement changed the installed binary\n' >&2; exit 1; }
cmp -s "$home/.local/bin/just-code.previous" "$temp_root/expected-target" || { printf 'rollback copy missing after failed replacement\n' >&2; exit 1; }

if run_installer "$home" "$good_sums" 0 0 9 > "$temp_root/setup-failure.out" 2>&1; then
	printf 'setup failure unexpectedly succeeded\n' >&2
	exit 1
fi
cmp -s "$target" "$fake_binary" || { printf 'setup failure rolled back the new binary unexpectedly\n' >&2; exit 1; }
[ "$(cat "$home/.local/bin/just-code.previous")" = 'keep me' ] || { printf 'setup failure did not retain the previous binary\n' >&2; exit 1; }
grep -F 'To roll back:' "$temp_root/setup-failure.out" >/dev/null || { printf 'setup failure omitted rollback guidance\n' >&2; exit 1; }

unsupported_home="$temp_root/unsupported"
mkdir -p "$unsupported_home"
: > "$temp_root/unsupported-urls"
if env FAKE_OS=Darwin FAKE_ARCH=x86_64 FAKE_ASSET=$asset FAKE_BINARY="$fake_binary" \
	FAKE_SUMS="$good_sums" FAKE_URL_LOG="$temp_root/unsupported-urls" \
	FAKE_RELEASE_URL="$release_url" FAKE_SETUP_LOG="$temp_root/setup-called" \
	FAKE_TARGET="$unsupported_home/just-code" REAL_MV="$real_mv" \
	HOME="$unsupported_home" PATH="$fake_bin:$PATH" \
	"$installer" > "$temp_root/unsupported.out" 2>&1; then
	printf 'unsupported macOS Intel target unexpectedly succeeded\n' >&2
	exit 1
fi
[ ! -s "$temp_root/unsupported-urls" ] || { printf 'unsupported target performed network requests\n' >&2; exit 1; }

blocked_home="$temp_root/home with blocked bin"
mkdir -p "$blocked_home/.local"
printf 'not a directory\n' > "$blocked_home/.local/bin"
: > "$temp_root/blocked-urls"
if env FAKE_OS=Linux FAKE_ARCH=x86_64 FAKE_ASSET=$asset FAKE_BINARY="$fake_binary" \
	FAKE_SUMS="$good_sums" FAKE_URL_LOG="$temp_root/blocked-urls" \
	FAKE_RELEASE_URL="$release_url" FAKE_SETUP_LOG="$temp_root/setup-called" \
	FAKE_TARGET="$blocked_home/.local/bin/just-code" REAL_MV="$real_mv" \
	HOME="$blocked_home" PATH="$fake_bin:$PATH" \
	"$installer" > "$temp_root/blocked.out" 2>&1; then
	printf 'an unusable installation directory unexpectedly succeeded\n' >&2
	exit 1
fi
[ ! -s "$temp_root/blocked-urls" ] || { printf 'permission failure performed network requests\n' >&2; exit 1; }

printf 'POSIX installer checks passed\n'
