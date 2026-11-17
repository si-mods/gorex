#!/usr/bin/env bash
# Writes SHA256SUMS of a GitHub release from the release's own assets and
# uploads it, replacing the one there.
#
#   checksums.sh <tag> [-R owner/repo] [--dry-run] [--out FILE] [--dir DIR]
#
# It lists the same files as mygo's -upload puts in the SHA256SUMS it
# makes (cmd/mygo/publish.go uploads, checksums.go writeChecksums): the disk
# image, the NSIS installer, .deb, .rpm, .tar.gz, .delta, install.sh and
# install.ps1; not the update-*.json manifests and not SHA256SUMS itself.
# The format is that of sha256sum, "<hash><two spaces><name>", sorted by
# name in byte order.
#
# --dry-run only writes the file (to --out, default DIR/SHA256SUMS); it
# uploads nothing. DIR keeps the downloads (default: a temporary directory).
# Needs gh, signed in or with GH_TOKEN; runs on bash 3.2 (macOS) and Git Bash.
set -euo pipefail

tag= repo= dry=0 out= dir=
while [ $# -gt 0 ]; do
  case $1 in
    -R | --repo) repo=$2; shift ;;
    --dry-run) dry=1 ;;
    --out) out=$2; shift ;;
    --dir) dir=$2; shift ;;
    -*) echo "unknown option $1" >&2; exit 2 ;;
    *) tag=$1 ;;
  esac
  shift
done
[ -n "$tag" ] || { echo "usage: checksums.sh <tag> [-R owner/repo] [--dry-run] [--out FILE] [--dir DIR]" >&2; exit 2; }
repo_args=()
[ -z "$repo" ] || repo_args=(--repo "$repo")

if [ -z "$dir" ]; then
  dir=$(mktemp -d)
  trap 'rm -rf "$dir"' EXIT
fi
mkdir -p "$dir"
[ -n "$out" ] || out=$dir/SHA256SUMS
mkdir -p "$(dirname "$out")"

sha256() {
  if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

names=$(gh release view "$tag" ${repo_args[@]+"${repo_args[@]}"} --json assets --jq '.assets[].name' | tr -d '\r' | LC_ALL=C sort -u)
: > "$out.tmp"
count=0
while IFS= read -r name; do
  case $name in
    install.sh | install.ps1 | *.dmg | *-setup-*.exe | *.deb | *.rpm | *.tar.gz | *.delta) ;;
    *) continue ;;
  esac
  gh release download "$tag" ${repo_args[@]+"${repo_args[@]}"} --pattern "$name" --dir "$dir" --clobber
  printf '%s  %s\n' "$(sha256 "$dir/$name")" "$name" >> "$out.tmp"
  count=$((count + 1))
done <<< "$names"
[ "$count" -gt 0 ] || { echo "release $tag has no files to checksum" >&2; exit 1; }
mv "$out.tmp" "$out"
echo "Wrote $out ($count files)"

if [ "$dry" = 1 ]; then
  echo "Dry run: nothing uploaded"
  exit 0
fi
# Upload under the name SHA256SUMS whatever --out is called.
if [ "$(basename "$out")" != SHA256SUMS ]; then
  cp "$out" "$dir/SHA256SUMS"
  out=$dir/SHA256SUMS
fi
gh release upload "$tag" "$out" ${repo_args[@]+"${repo_args[@]}"} --clobber
