#!/usr/bin/env bash
# Installs stampede for the Stampede action (see resolve.sh for the modes)
# and puts it on the PATH of the following steps.
set -euo pipefail

repo="Ivan825/Stampede"
mode="${STAMPEDE_MODE:?}"
ref="${STAMPEDE_REF:-}"
bin_dir="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/stampede-bin"
mkdir -p "$bin_dir"

case "${RUNNER_OS:-$(uname -s)}" in
  Linux) os=linux ;;
  macOS | Darwin) os=darwin ;;
  Windows | MINGW* | MSYS*) os=windows ;;
  *) echo "::error title=Stampede::unsupported runner OS ${RUNNER_OS:-$(uname -s)}"; exit 1 ;;
esac
case "${RUNNER_ARCH:-$(uname -m)}" in
  X64 | x86_64 | amd64) arch=amd64 ;;
  ARM64 | arm64 | aarch64) arch=arm64 ;;
  *) echo "::error title=Stampede::unsupported runner architecture ${RUNNER_ARCH:-$(uname -m)}"; exit 1 ;;
esac
exe=stampede
if [ "$os" = windows ]; then
  exe=stampede.exe
fi

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

case "$mode" in
  release)
    v="${ref#v}"
    ext=tar.gz
    if [ "$os" = windows ]; then
      ext=zip
    fi
    asset="stampede_${v}_${os}_${arch}.${ext}"
    base="https://github.com/${repo}/releases/download/${ref}"
    tmp=$(mktemp -d)
    echo "Downloading ${base}/${asset}"
    curl -fsSL --retry 3 -o "${tmp}/${asset}" "${base}/${asset}"
    curl -fsSL --retry 3 -o "${tmp}/checksums.txt" "${base}/stampede_${v}_checksums.txt"
    want=$(awk -v a="$asset" '$2 == a { print $1 }' "${tmp}/checksums.txt")
    got=$(sha256 "${tmp}/${asset}")
    if [ -z "$want" ] || [ "$want" != "$got" ]; then
      echo "::error title=Stampede::checksum mismatch for ${asset} (want ${want:-none}, got ${got})"
      exit 1
    fi
    if [ "$ext" = zip ]; then
      unzip -q -o "${tmp}/${asset}" -d "${tmp}/x"
    else
      mkdir -p "${tmp}/x"
      tar -xzf "${tmp}/${asset}" -C "${tmp}/x"
    fi
    cp "$(find "${tmp}/x" -name "$exe" -type f | head -n 1)" "${bin_dir}/${exe}"
    chmod +x "${bin_dir}/${exe}"
    ;;
  source)
    echo "Building github.com/${repo}/cmd/stampede@${ref}"
    GOBIN="$bin_dir" go install "github.com/${repo}/cmd/stampede@${ref}"
    ;;
  local)
    echo "Building the Stampede repository in ${GITHUB_WORKSPACE:-$PWD}"
    (cd "${GITHUB_WORKSPACE:-$PWD}" && GOBIN="$bin_dir" go install ./cmd/stampede)
    ;;
  *)
    echo "::error title=Stampede::unknown install mode ${mode}"
    exit 1
    ;;
esac

if [ -n "${GITHUB_PATH:-}" ]; then
  echo "$bin_dir" >>"$GITHUB_PATH"
fi
"${bin_dir}/${exe}" version
