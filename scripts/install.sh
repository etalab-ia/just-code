#!/bin/sh
set -eu

REPOSITORY='etalab-ia/just-code'
RELEASE_PAGE="https://github.com/$REPOSITORY/releases/latest"
RELEASE_PREFIX="https://github.com/$REPOSITORY/releases/tag/"
DOWNLOAD_PREFIX="https://github.com/$REPOSITORY/releases/download"

die() {
	printf 'just-code installer: %s\n' "$1" >&2
	exit 1
}

system=$(uname -s) || die 'could not detect the operating system'
machine=$(uname -m) || die 'could not detect the machine architecture'
case "$system:$machine" in
	Darwin:arm64|Darwin:aarch64)
		asset='just-code-darwin-arm64'
		;;
	Darwin:*)
		die "unsupported target $system/$machine; macOS Intel binaries are not published"
		;;
	Linux:x86_64|Linux:amd64)
		asset='just-code-linux-x64'
		;;
	Linux:arm64|Linux:aarch64)
		asset='just-code-linux-arm64'
		;;
	Linux:*)
		die "unsupported target $system/$machine"
		;;
	*)
		die "unsupported operating system $system; use install.ps1 on Windows"
		;;
esac

if [ -z "${HOME:-}" ]; then
	die 'HOME is not set; cannot choose a user-owned installation directory'
fi
install_dir="$HOME/.local/bin"
mkdir -p "$install_dir" || die "cannot create $install_dir; choose a writable user directory"
tmp_dir=$(mktemp -d "$install_dir/.just-code-install.XXXXXX") || die "cannot create a temporary directory under $install_dir"
trap 'rm -rf "$tmp_dir"' 0
trap 'exit 1' HUP INT TERM

target="$install_dir/just-code"
backup="$install_dir/just-code.previous"
if [ -L "$target" ]; then
	die "$target is a symlink; refusing to replace it"
fi
if [ -e "$target" ] && [ ! -f "$target" ]; then
	die "$target is not a regular file; refusing to replace it"
fi

release_url=$(curl --fail --silent --show-error --location --output /dev/null --write-out '%{url_effective}' "$RELEASE_PAGE") || die 'could not resolve the latest stable GitHub release'
case "$release_url" in
	"$RELEASE_PREFIX"*) tag=${release_url#"$RELEASE_PREFIX"} ;;
	*) die "unexpected release URL: $release_url" ;;
esac
printf '%s\n' "$tag" | grep -Eq '^just-code-v?[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$' || die "unexpected release tag: $tag"

download() {
	url=$1
	destination=$2
	curl --fail --silent --show-error --location --output "$destination" "$url" || die "download failed: $url"
	[ -s "$destination" ] || die "download was empty: $url"
}

download "$DOWNLOAD_PREFIX/$tag/$asset" "$tmp_dir/$asset"
download "$DOWNLOAD_PREFIX/$tag/SHA256SUMS" "$tmp_dir/SHA256SUMS"

expected=$(awk -v asset="$asset" '
	{
		name = $2
		sub(/^\*/, "", name)
		if (name == asset) {
			count++
			hash = $1
		}
	}
	END {
		if (count != 1) exit 1
		print hash
	}
' "$tmp_dir/SHA256SUMS") || die "SHA256SUMS does not contain exactly one entry for $asset"
case "$expected" in
	*[!0123456789abcdefABCDEF]*|'') die "invalid checksum entry for $asset" ;;
esac
[ "${#expected}" -eq 64 ] || die "invalid checksum length for $asset"

if command -v sha256sum >/dev/null 2>&1; then
	hash_output=$(sha256sum "$tmp_dir/$asset") || die 'sha256sum failed'
elif command -v shasum >/dev/null 2>&1; then
	hash_output=$(shasum -a 256 "$tmp_dir/$asset") || die 'shasum failed'
else
	die 'no SHA-256 tool found (need sha256sum or shasum)'
fi
actual=$(printf '%s\n' "$hash_output" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')
expected=$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')
[ "$actual" = "$expected" ] || die "checksum mismatch for $asset; existing installation was not changed"

chmod 0755 "$tmp_dir/$asset" || die 'could not mark the verified binary executable'
if [ -e "$target" ]; then
	cp -p "$target" "$tmp_dir/just-code.previous" || die 'could not preserve the current binary for rollback'
	mv -f "$tmp_dir/just-code.previous" "$backup" || die 'could not save the rollback binary; current installation was not changed'
fi
mv -f "$tmp_dir/$asset" "$target" || die "could not replace $target; the previous binary remains available"

if "$target" setup; then
	:
else
	status=$?
	printf 'just-code was installed, but setup exited with status %s.\n' "$status" >&2
	if [ -f "$backup" ]; then
		printf "To roll back: mv \"\$HOME/.local/bin/just-code.previous\" \"\$HOME/.local/bin/just-code\"\n" >&2
	fi
	exit "$status"
fi

printf 'Installed %s from release %s at %s\n' "$asset" "$tag" "$target"
case ":${PATH:-}:" in
	*":$install_dir:"*) ;;
	*)
		printf "Add ~/.local/bin to PATH for future shells; for this shell run:\n  export PATH=\"\$HOME/.local/bin:\$PATH\"\n"
		printf 'The installer does not edit shell profile files.\n'
		;;
esac
