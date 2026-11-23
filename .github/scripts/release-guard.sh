#!/usr/bin/env bash
# At a release run's start, on the instabox runner (release.yml's guard
# job):
#
#  * cancels every older run of this workflow still in flight or queued,
#    and names it and the reason on this run's page: the cancelled run's
#    page cannot carry one. A newer dispatch supersedes it — its apps and
#    its draft would be of an older main (0.53.0's assets were of the
#    release commit while its tag, unpinned then, landed on a newer one).
#    Concurrency's cancel-in-progress makes the kill itself; the loop is
#    a net for a cancellation still propagating when this job starts.
#  * refuses a dispatch whose commit main has left while the run queued:
#    its apps would ship the older code, so exit 1 and dispatch again.
#
# Run it at the repository root. Needs gh and jq; GITHUB_* of the Actions
# run.
set -euo pipefail

repo=$GITHUB_REPOSITORY
me=$GITHUB_RUN_ID
url="$GITHUB_SERVER_URL/$repo/actions/runs/$me"
mine=$(gh api "repos/$repo/actions/runs/$me" | jq -r .created_at)

wf="repos/$repo/actions/workflows/release.yml/runs"

runs() { # status -> tsv id, url, sha, created_at of runs older than this
	gh api "$wf?status=$1&per_page=30" | jq -r --arg me "$me" '
	  .workflow_runs[]
	  | select((.id | tostring) != $me and .id < ($me | tonumber))
	  | [.id, .html_url, .head_sha[0:8], .created_at] | @tsv'
}

# An id grows with the dispatch order, so the runs this one supersedes are
# exactly the older ones: a newer run never meets this job — the group
# runs one at a time and cancels this instead.
killed=
for st in in_progress queued; do
	while IFS=$'\t' read -r id run_url sha at; do
		[ -n "$id" ] || continue
		gh api -X POST "repos/$repo/actions/runs/$id/cancel" >/dev/null
		killed="$killed- $run_url — sha \`$sha\`, dispatched $at — superseded by $url, a newer dispatch"$'\n'
		echo "::warning::Cancelled release run $run_url (sha $sha, dispatched $at): superseded by a newer dispatch $url"
	done < <(runs "$st")
done

# Concurrency may have made the kill before this job's query: name those
# too. A run it cancelled has its updated_at after this run's created_at.
gone=$(gh api "$wf?status=completed&per_page=30" | jq -r --arg me "$me" --arg mine "$mine" '
  .workflow_runs[]
  | select(.conclusion == "cancelled" and (.id | tostring) != $me and .updated_at > $mine)
  | "- \(.html_url) — sha `\(.head_sha[0:8])`, dispatched \(.created_at)"')

if [ -n "$killed" ] || [ -n "$gone" ]; then
	{
		echo "### Superseded release runs"
		echo
		echo "A newer dispatch cancels an older release run in flight or queued."
		echo
		[ -n "$killed" ] && printf '%s' "$killed"
		[ -n "$gone" ] && printf '%s\n' "$gone"
	} >> "$GITHUB_STEP_SUMMARY"
fi

head=$(gh api "repos/$repo/commits/$GITHUB_REF_NAME" | jq -r .sha)
if [ "$head" != "$GITHUB_SHA" ]; then
	{
		echo "### Stale dispatch"
		echo
		echo "main moved to \`${head:0:8}\` since this dispatch of \`${GITHUB_SHA:0:8}\`. Its apps would be of the older commit; dispatch again on the newer main."
	} >> "$GITHUB_STEP_SUMMARY"
	echo "::error::Stale dispatch: main is ${head:0:8}, this run builds ${GITHUB_SHA:0:8}. Dispatch again."
	exit 1
fi
echo "guard: building main's head ${GITHUB_SHA:0:8}"
