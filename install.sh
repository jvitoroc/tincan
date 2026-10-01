#!/bin/sh
# Install or update tincan from GitHub Releases.
#
#   curl -fsSL https://raw.githubusercontent.com/jvitoroc/tincan/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/jvitoroc/tincan/main/install.sh | sh -s -- --server
#
# Running it again updates to the latest release.
#
# Everything is inside main(), which is called on the last line, so a
# partially downloaded script never runs.

set -eu

REPO=jvitoroc/tincan
RELEASES_URL="https://github.com/$REPO/releases"

usage() {
	cat <<EOF
Install or update tincan from GitHub Releases.

Usage:
  curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sh
  curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sh -s -- --server

Options:
  --server     install the server (tincan-server) instead of the client (tincan)
  -h, --help   show this help

Environment:
  TINCAN_VERSION      release tag to install, e.g. v0.2.0 (default: latest)
  TINCAN_INSTALL_DIR  directory to install into (default: \$HOME/.local/bin)
  TINCAN_BASE_URL     download assets from this URL instead of GitHub Releases
EOF
}

say() {
	printf '%s\n' "$*"
}

warn() {
	printf 'tincan install: warning: %s\n' "$*" >&2
}

die() {
	printf 'tincan install: error: %s\n' "$*" >&2
	exit 1
}

unsupported() {
	cat >&2 <<EOF
tincan install: error: no prebuilt $bin for this platform ($1).

Prebuilt binaries exist for macOS and Linux (amd64 and arm64), plus a
Windows .zip you can download by hand:
  $RELEASES_URL

If you have Go and a C compiler, you can build it yourself instead:
  go install github.com/$REPO/cmd/$bin@latest
EOF
	exit 1
}

have() {
	command -v "$1" >/dev/null 2>&1
}

# download URL FILE
download() {
	if have curl; then
		curl -fsSL --retry 3 -o "$2" "$1"
	else
		wget -qO "$2" "$1"
	fi
}

# sha256 FILE: prints the file's hex SHA-256.
sha256() {
	if have sha256sum; then
		sha256sum "$1" | cut -d ' ' -f 1
	else
		shasum -a 256 "$1" | cut -d ' ' -f 1
	fi
}

tmp=""
staged=""
cleanup() {
	if [ -n "$staged" ]; then
		rm -f "$staged"
	fi
	if [ -n "$tmp" ]; then
		rm -rf "$tmp"
	fi
}

# path_hint DIR: tells the user how to put DIR on PATH.
# shellcheck disable=SC2088 # the ~ paths are printed, not expanded
path_hint() {
	dir=$1
	if [ -n "${HOME:-}" ]; then
		case $1 in
		"$HOME"/*) dir="\$HOME${1#"$HOME"}" ;;
		esac
	fi
	line="export PATH=\"$dir:\$PATH\""
	case ${SHELL:-} in
	*/zsh) profile="~/.zshrc" ;;
	*/bash)
		if [ "$os" = darwin ]; then
			profile="~/.bash_profile"
		else
			profile="~/.bashrc"
		fi
		;;
	*/fish)
		profile="~/.config/fish/config.fish"
		line="fish_add_path $dir"
		;;
	*) profile="~/.profile" ;;
	esac
	say ""
	say "$1 is not on your PATH. Add this line to $profile:"
	say ""
	say "  $line"
	say ""
	say "then open a new terminal."
}

main() {
	bin=tincan
	for arg in "$@"; do
		case $arg in
		--server) bin=tincan-server ;;
		-h | --help)
			usage
			exit 0
			;;
		*)
			printf 'tincan install: unknown option: %s\n\n' "$arg" >&2
			usage >&2
			exit 2
			;;
		esac
	done

	have curl || have wget || die "curl or wget is required"
	have sha256sum || have shasum || die "sha256sum or shasum is required to verify the download"
	have tar || die "tar is required"

	uname_s=$(uname -s)
	uname_m=$(uname -m)
	case $uname_s in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) unsupported "$uname_s $uname_m" ;;
	esac
	case $uname_m in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) unsupported "$uname_s $uname_m" ;;
	esac
	# A shell running under Rosetta reports x86_64 on Apple Silicon.
	if [ "$os" = darwin ] && [ "$arch" = amd64 ] &&
		[ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
		arch=arm64
	fi

	if [ "$bin" = tincan ] && [ "$os" = darwin ]; then
		asset=tincan_darwin_universal.tar.gz
	else
		asset=${bin}_${os}_${arch}.tar.gz
	fi

	version=${TINCAN_VERSION:-latest}
	if [ -n "${TINCAN_BASE_URL:-}" ]; then
		base=${TINCAN_BASE_URL%/}
	elif [ "$version" = latest ]; then
		base="$RELEASES_URL/latest/download"
	else
		case $version in
		v*) ;;
		*) version=v$version ;;
		esac
		base="$RELEASES_URL/download/$version"
	fi

	install_dir=${TINCAN_INSTALL_DIR:-}
	if [ -z "$install_dir" ]; then
		[ -n "${HOME:-}" ] || die "HOME is not set; set TINCAN_INSTALL_DIR"
		install_dir=$HOME/.local/bin
	fi
	[ "$install_dir" = / ] || install_dir=${install_dir%/}
	dest=$install_dir/$bin
	[ ! -d "$dest" ] || die "$dest is a directory"

	trap cleanup EXIT
	trap 'exit 1' HUP INT TERM
	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t tincan)

	say "Downloading $asset ($version)"
	download "$base/$asset" "$tmp/$asset" ||
		die "could not download $base/$asset"
	download "$base/checksums.txt" "$tmp/checksums.txt" ||
		die "could not download $base/checksums.txt"

	want=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/checksums.txt")
	[ -n "$want" ] || die "$asset is not listed in checksums.txt; refusing to install"
	got=$(sha256 "$tmp/$asset")
	if [ "$got" != "$want" ]; then
		die "checksum mismatch for $asset (expected $want, got $got); refusing to install"
	fi

	mkdir "$tmp/x"
	tar -xzf "$tmp/$asset" -C "$tmp/x" "$bin" ||
		die "could not extract $bin from $asset"
	[ -f "$tmp/x/$bin" ] || die "$asset does not contain $bin"

	mkdir -p "$install_dir" || die "could not create $install_dir"
	# Copy next to the destination, then rename over it. The rename is
	# atomic, so a running tincan keeps working and the update is never
	# half-written. A new inode also keeps macOS from killing the new
	# binary over a stale code-signature cache entry.
	staged=$install_dir/.$bin.install.$$
	cp "$tmp/x/$bin" "$staged" || die "could not write to $install_dir"
	chmod 755 "$staged"
	mv -f "$staged" "$dest" || die "could not replace $dest"
	staged=""

	installed=$("$dest" --version </dev/null 2>&1) ||
		die "installed $dest, but it failed to run: $installed"
	say "Installed $installed to $dest"

	case ":$PATH:" in
	*":$install_dir:"*)
		found=$(command -v "$bin" 2>/dev/null || true)
		if [ -n "$found" ] && [ "$found" != "$dest" ]; then
			warn "$found comes before $dest on your PATH, so '$bin' still runs the old copy"
		fi
		;;
	*) path_hint "$install_dir" ;;
	esac
}

main "$@"
