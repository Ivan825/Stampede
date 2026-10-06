#!/usr/bin/env bash
# Decides how the Stampede action installs stampede and writes mode and ref
# to $GITHUB_OUTPUT:
#   release  download the release binary for tag "ref"
#   source   go install github.com/Ivan825/Stampede/cmd/stampede@ref
#   local    build the repository checked out in the workspace
set -euo pipefail

repo="Ivan825/Stampede"
version="${STAMPEDE_VERSION:-latest}"
out="${GITHUB_OUTPUT:-/dev/stdout}"

mode=source
ref="$version"
case "$version" in
  local)
    mode=local
    ref=""
    ;;
  latest)
    auth=()
    if [ -n "${GITHUB_TOKEN:-}" ]; then
      auth=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
    fi
    tag=$(curl -fsSL ${auth[@]+"${auth[@]}"} -H "Accept: application/vnd.github+json" \
      "https://api.github.com/repos/${repo}/releases/latest" 2>/dev/null |
      sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1 || true)
    if [ -n "$tag" ]; then
      mode=release
      ref="$tag"
    else
      echo "No Stampede release is published yet; building the latest commit from source."
    fi
    ;;
  v[0-9]*.[0-9]*.[0-9]*)
    mode=release
    ;;
esac

echo "Stampede install: mode=${mode} ref=${ref:-workspace}"
{
  echo "mode=${mode}"
  echo "ref=${ref}"
} >>"$out"
