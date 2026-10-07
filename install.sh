#!/bin/sh
# Installs the stampede CLI (one binary: CLI, server, worker and console).
#
#   curl -fsSL https://raw.githubusercontent.com/Ivan825/Stampede/main/install.sh | sh
#
# It downloads the latest release for this OS and CPU from GitHub, checks
# it against the release's SHA-256 checksums and puts it in
# ~/.local/bin (or /usr/local/bin when that is writable and ~/.local/bin is
# not on PATH). With no release published yet, it builds from source with
# Go instead, if Go is installed.
#
# Settings (environment variables):
#   STAMPEDE_VERSION      a release tag such as v1.0.0 (default: latest)
#   STAMPEDE_INSTALL_DIR  where to put the binary
#   STAMPEDE_DOWNLOAD_BASE  download from this URL instead of the GitHub
#                         release (a mirror; it must hold the same files)
set -eu

repo="Ivan825/Stampede"
version="${STAMPEDE_VERSION:-}"

say() { printf '%s\n' "$*"; }
fail() { say "stampede install: $*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

have curl || fail "curl is required"
have tar || fail "tar is required"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  MINGW* | MSYS* | CYGWIN*) fail "on Windows, use install.ps1 (see the README)" ;;
  *) fail "unsupported OS $(uname -s); build from source with: go install github.com/$repo/cmd/stampede@latest" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported CPU $(uname -m); build from source with: go install github.com/$repo/cmd/stampede@latest" ;;
esac

# Where to install: an explicit choice, else ~/.local/bin, else
# /usr/local/bin when it is writable and ~/.local/bin is not on PATH.
dir="${STAMPEDE_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  dir="$HOME/.local/bin"
  case ":$PATH:" in
    *":$dir:"*) ;;
    *) if [ -w /usr/local/bin ]; then dir=/usr/local/bin; fi ;;
  esac
fi
mkdir -p "$dir"

sha256() {
  if have sha256sum; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

from_source() {
  have go || fail "no release is published yet and Go is not installed. Install Go (https://go.dev/dl/) and run this again, or use Docker Compose (see the README)."
  say "No release is published yet: building from source with Go (this takes a minute)."
  GOBIN="$dir" GOTOOLCHAIN=auto go install "github.com/$repo/cmd/stampede@${version:-latest}"
}

if [ -z "$version" ] && [ -z "${STAMPEDE_DOWNLOAD_BASE:-}" ]; then
  version=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" 2>/dev/null |
    sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1) || true
fi

if [ -z "$version" ]; then
  from_source
else
  v="${version#v}"
  base="${STAMPEDE_DOWNLOAD_BASE:-https://github.com/$repo/releases/download/$version}"
  asset="stampede_${v}_${os}_${arch}.tar.gz"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  say "Downloading stampede $version for $os/$arch"
  curl -fsSL --retry 3 -o "$tmp/$asset" "$base/$asset" || fail "could not download $base/$asset"
  curl -fsSL --retry 3 -o "$tmp/checksums.txt" "$base/stampede_${v}_checksums.txt" || fail "could not download the checksums"
  want=$(awk -v a="$asset" '$2 == a { print $1 }' "$tmp/checksums.txt")
  got=$(sha256 "$tmp/$asset")
  [ -n "$want" ] && [ "$want" = "$got" ] || fail "checksum mismatch for $asset (want ${want:-none}, got $got)"
  tar -xzf "$tmp/$asset" -C "$tmp"
  install -m 0755 "$tmp/stampede" "$dir/stampede" 2>/dev/null || {
    cp "$tmp/stampede" "$dir/stampede" && chmod 0755 "$dir/stampede"
  }
fi

say "Installed $("$dir/stampede" version | head -n 1) to $dir/stampede"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) say "Add it to your PATH:  export PATH=\"$dir:\$PATH\"" ;;
esac
say ""
say "Next:"
say "  stampede init --target http://localhost:3000   # detect your app, install a pack, dry-run it"
say "  stampede                                        # the interactive console"
say "  stampede up                                     # the full stack with the web UI (needs Docker)"
