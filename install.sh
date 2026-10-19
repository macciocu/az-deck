#!/bin/sh
# azd installer for Linux, macOS and Windows (WSL).
#
#   curl -fsSL https://raw.githubusercontent.com/macciocu/az-deck/main/install.sh | sh
#
# Installs a prebuilt binary from GitHub Releases into ~/.local/bin (no sudo).
# Options (flags or environment variables):
#   --version <tag>   AZD_VERSION      release tag to install (default: latest)
#   --dir <path>      AZD_INSTALL_DIR  install directory (default: ~/.local/bin)
#   --name <name>     AZD_BIN_NAME     binary name (default: azd)
#   --from-source     AZD_FROM_SOURCE=1 build from source with Docker instead
#   --uninstall                        remove the installed binary
set -eu

REPO="${AZD_REPO:-macciocu/az-deck}"
VERSION="${AZD_VERSION:-latest}"
INSTALL_DIR="${AZD_INSTALL_DIR:-$HOME/.local/bin}"
BIN_NAME="${AZD_BIN_NAME:-azd}"
FROM_SOURCE="${AZD_FROM_SOURCE:-0}"
BASE_URL="${AZD_BASE_URL:-https://github.com/$REPO/releases/download}"
GO_IMAGE="${AZD_GO_IMAGE:-golang:1.26-alpine}"
UNINSTALL=0

info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33mwarning:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }

usage() { sed -n '2,15p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'; exit 0; }

while [ $# -gt 0 ]; do
	case "$1" in
	--version) VERSION="$2"; shift 2 ;;
	--dir) INSTALL_DIR="$2"; shift 2 ;;
	--name) BIN_NAME="$2"; shift 2 ;;
	--from-source) FROM_SOURCE=1; shift ;;
	--uninstall) UNINSTALL=1; shift ;;
	-h | --help) usage ;;
	*) die "unknown option: $1" ;;
	esac
done

TARGET="$INSTALL_DIR/$BIN_NAME"

if [ "$UNINSTALL" = 1 ]; then
	if [ -f "$TARGET" ]; then
		rm -f "$TARGET"
		info "removed $TARGET"
	else
		warn "$TARGET not found"
	fi
	info "settings (if any) remain in ${XDG_CONFIG_HOME:-$HOME/.config}/azd"
	exit 0
fi

# ---- platform detection ------------------------------------------------------
case "$(uname -s)" in
Linux) OS=linux ;;
Darwin) OS=darwin ;;
MINGW* | MSYS* | CYGWIN*) die "native Windows is not supported; install inside WSL (https://aka.ms/wsl)" ;;
*) die "unsupported OS: $(uname -s)" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) die "unsupported architecture: $(uname -m)" ;;
esac
WSL=0
if [ "$OS" = linux ] && grep -qi microsoft /proc/version 2>/dev/null; then
	WSL=1
fi

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
	fetch_stdout() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$2" "$1"; }
	fetch_stdout() { wget -qO- "$1"; }
else
	die "curl or wget is required"
fi

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

# ---- install from a release --------------------------------------------------
install_release() {
	if [ "$VERSION" = latest ]; then
		VERSION="$(fetch_stdout "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null |
			sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)" || true
		[ -n "$VERSION" ] || return 1
	fi
	ver="${VERSION#v}"
	archive="azd_${ver}_${OS}_${ARCH}.tar.gz"
	info "downloading azd $VERSION ($OS/$ARCH)"
	fetch "$BASE_URL/$VERSION/$archive" "$TMP/$archive" || return 1
	if fetch "$BASE_URL/$VERSION/checksums.txt" "$TMP/checksums.txt" 2>/dev/null; then
		want="$(grep " $archive\$" "$TMP/checksums.txt" | awk '{print $1}')"
		got="$(sha256 "$TMP/$archive")"
		[ -n "$want" ] && [ "$want" = "$got" ] || die "checksum mismatch for $archive"
		info "checksum verified"
	else
		warn "no checksums.txt published; skipping verification"
	fi
	tar -xzf "$TMP/$archive" -C "$TMP"
	[ -f "$TMP/azd" ] || die "archive did not contain an azd binary"
	BUILT="$TMP/azd"
}

# ---- build from source in Docker ---------------------------------------------
install_source() {
	command -v docker >/dev/null 2>&1 || die "--from-source needs Docker (nothing is installed on your system besides the azd binary)"
	docker info >/dev/null 2>&1 || die "Docker is installed but not running"
	src="$TMP/src"
	script_dir="$(cd "$(dirname "$0")" 2>/dev/null && pwd || true)"
	if [ -n "$script_dir" ] && [ -f "$script_dir/go.mod" ] && grep -q "module github.com/$REPO" "$script_dir/go.mod"; then
		src="$script_dir"
		info "building from local checkout $src"
	else
		ref="main"
		[ "$VERSION" != latest ] && ref="$VERSION"
		info "downloading source ($ref)"
		mkdir -p "$src"
		fetch "https://github.com/$REPO/archive/$ref.tar.gz" "$TMP/src.tar.gz" || die "could not download source for $ref"
		tar -xzf "$TMP/src.tar.gz" -C "$src" --strip-components=1
	fi
	info "compiling for $OS/$ARCH in $GO_IMAGE"
	mkdir -p "$TMP/out"
	docker run --rm -v "$src":/src:ro -v "$TMP/out":/out -w /src \
		-e CGO_ENABLED=0 -e GOOS="$OS" -e GOARCH="$ARCH" -e GOFLAGS="-buildvcs=false -modcacherw" \
		"$GO_IMAGE" go build -trimpath -ldflags "-s -w -X main.version=${VERSION}-src" -o /out/azd ./cmd/azd
	BUILT="$TMP/out/azd"
}

BUILT=""
if [ "$FROM_SOURCE" = 1 ]; then
	install_source
elif ! install_release; then
	warn "no release found for $REPO ($VERSION)"
	if command -v docker >/dev/null 2>&1; then
		info "falling back to building from source with Docker"
		install_source
	else
		die "no prebuilt release available and Docker is not installed for a source build"
	fi
fi

# ---- name clash with the Azure Developer CLI ---------------------------------
existing="$(command -v "$BIN_NAME" 2>/dev/null || true)"
if [ -n "$existing" ] && [ "$existing" != "$TARGET" ]; then
	if ! "$existing" -version 2>/dev/null | grep -q "^azd .*(commit"; then
		warn "another '$BIN_NAME' exists at $existing (probably the Azure Developer CLI)."
		warn "whichever comes first in PATH wins. Re-run with AZD_BIN_NAME=azdeck to avoid the clash."
	fi
fi

mkdir -p "$INSTALL_DIR"
install -m 0755 "$BUILT" "$TARGET" 2>/dev/null || { cp "$BUILT" "$TARGET" && chmod 0755 "$TARGET"; }
if [ "$OS" = darwin ] && command -v xattr >/dev/null 2>&1; then
	xattr -d com.apple.quarantine "$TARGET" 2>/dev/null || true
fi
info "installed $("$TARGET" -version) to $TARGET"

case ":$PATH:" in
*":$INSTALL_DIR:"*) ;;
*)
	shell_rc="$HOME/.profile"
	case "${SHELL:-}" in
	*zsh) shell_rc="$HOME/.zshrc" ;;
	*bash) shell_rc="$HOME/.bashrc" ;;
	*fish) shell_rc="$HOME/.config/fish/config.fish" ;;
	esac
	warn "$INSTALL_DIR is not on your PATH. Add it with:"
	case "${SHELL:-}" in
	*fish) printf '    fish_add_path %s\n' "$INSTALL_DIR" ;;
	*)
		# shellcheck disable=SC2016
		printf '    echo '\''export PATH="%s:$PATH"'\'' >> %s\n' "$INSTALL_DIR" "$shell_rc"
		;;
	esac
	;;
esac

if ! command -v az >/dev/null 2>&1; then
	warn "the Azure CLI was not found. azd authenticates via 'az login' (or AZURE_* env vars / managed identity)."
	if [ "$WSL" = 1 ]; then
		warn "inside WSL, install the Linux Azure CLI: https://aka.ms/InstallAzureCLIDeb"
	fi
fi
info "run '$BIN_NAME' to start (press ? for help)"
