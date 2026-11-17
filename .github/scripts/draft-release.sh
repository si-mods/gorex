#!/usr/bin/env bash
# Creates the draft GitHub release of mygo.json's version, as mygo's
# -upload does (cmd/mygo/publish.go publishGitHub): tag and title v<version>,
# notes from the "## <version>" section of CHANGELOG.md. It does nothing when
# the release exists. Run it before the jobs that upload, so that they
# find the release and none creates it at the same time as another.
#
#   draft-release.sh [-R owner/repo] [--dry-run]
#
# --dry-run prints the title and notes and touches nothing on GitHub.
# Run it at the repository root. Needs gh and jq.
set -euo pipefail

repo= dry=0
while [ $# -gt 0 ]; do
  case $1 in
    -R | --repo) repo=$2; shift ;;
    --dry-run) dry=1 ;;
    *) echo "unknown option $1" >&2; exit 2 ;;
  esac
  shift
done
[ -n "$repo" ] || repo=$(jq -r .updates.github mygo.json)
version=$(jq -r .version mygo.json)
tag=v$version

# Same as update.ReleaseNotes: the lines under "## <version>", also
# "## [<version>]" and "## [<version>](link)", or with text after the
# version, up to the next "## " heading, trimmed.
notes=$(tr -d '\r' < CHANGELOG.md | awk -v v="$version" '
  /^## / {
    if (in_section) exit
    split(substr($0, 4), f, /[ \t]+/)
    h = f[1]
    if (h == "") h = f[2]
    if (substr(h, 1, 1) == "[") { h = substr(h, 2); sub(/\].*/, "", h) }
    sub(/^v/, "", h)
    in_section = (h == v)
    next
  }
  in_section { print }
')
# Trim blank lines at both ends.
notes=$(printf '%s\n' "$notes" | awk 'NF { started = 1 } started { lines[++n] = $0 } END { while (n && lines[n] ~ /^[ \t]*$/) n--; for (i = 1; i <= n; i++) print lines[i] }')
[ -n "$notes" ] || { echo "CHANGELOG.md has no notes for $version: add a \"## $version\" section" >&2; exit 1; }

if [ "$dry" = 1 ]; then
  printf '%s\n' "$tag" "$notes"
  exit 0
fi
if gh release view "$tag" --repo "$repo" >/dev/null 2>&1; then
  echo "Release $tag of $repo exists"
  exit 0
fi
gh release create "$tag" --repo "$repo" --draft --title "$tag" --notes "$notes"
