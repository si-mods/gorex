#!/usr/bin/env bash
# At a TestFlight run's start, on the instabox runner (testflight.yml's
# guard job): cancels every older run of this workflow still in flight or
# queued, and names it and the reason on this run's page — the cancelled
# run's page cannot carry one. This run is of a newer main, so it uploads
# the older run's commits too; the older upload would only spend the Mac
# mini and a build number. Concurrency's cancel-in-progress makes the kill
# itself; the loop is a net for a cancellation still propagating when this
# job starts.
#
# A run killed while it exports gives up a build number at most: the next
# upload takes the next one (ios/scripts/upload).
#
#   testflight-guard.sh
#
# DRY_RUN=1 prints the cancels instead of making them, and the summary to
# stdout when GITHUB_STEP_SUMMARY is unset. Needs gh and jq; GITHUB_* of
# the Actions run.
set -euo pipefail

repo=$GITHUB_REPOSITORY
me=$GITHUB_RUN_ID
summary=${GITHUB_STEP_SUMMARY:-/dev/stdout}
url="$GITHUB_SERVER_URL/$repo/actions/runs/$me"
mine=$(gh api "repos/$repo/actions/runs/$me" --jq .created_at)

wf="repos/$repo/actions/workflows/testflight.yml/runs"

act() { # runs a command that changes GitHub, or prints it under DRY_RUN
	if [ "${DRY_RUN:-}" = 1 ]; then
		echo "dry-run: $*" >&2
	else
		"$@" >&2
	fi
}

runs() { # status -> tsv id, url, sha, created_at of runs older than this
	gh api "$wf?status=$1&per_page=30" | jq -r --arg me "$me" '
	  .workflow_runs[]
	  | select(.id < ($me | tonumber))
	  | [.id, .html_url, .head_sha, .created_at] | @tsv'
}

# by_group id: the testflight group cancelled run id for a newer one.
# GitHub says so in an annotation on its jobs; a run cancelled by hand has
# none. A run the group replaced while it waited has no jobs, and no
# annotation: it was cancelled within seconds of a newer run's start.
by_group() {
	local job jobs upd
	jobs=$(gh api "repos/$repo/actions/runs/$1/jobs" --jq '.jobs[].id')
	if [ -z "$jobs" ]; then
		upd=$(gh api "repos/$repo/actions/runs/$1" --jq .updated_at)
		gh api "$wf?per_page=50" | jq -e --arg id "$1" --arg upd "$upd" '
		  [.workflow_runs[]
		   | select(.id > ($id | tonumber))
		   | ($upd | fromdate) - (.created_at | fromdate)
		   | select(. >= 0 and . <= 10)]
		  | length > 0' >/dev/null
		return
	fi
	for job in $jobs; do
		gh api "repos/$repo/check-runs/$job/annotations" --jq '.[].message' |
			grep -q 'higher priority waiting request' && return 0
	done
	return 1
}

lines= seen=" "
note() { # url sha at reason
	lines="$lines- $1 — sha \`${2:0:8}\`, started $3 — $4"$'\n'
}

# An id grows with the start order, so the runs this one supersedes are
# exactly the older ones: a newer run never meets this job — the group
# cancels this one instead.
for st in in_progress queued waiting pending requested; do
	while IFS=$'\t' read -r id run_url sha at; do
		[ -n "$id" ] || continue
		seen="$seen$id "
		act gh api -X POST "repos/$repo/actions/runs/$id/cancel"
		echo "::warning::Cancelled TestFlight run $run_url (sha ${sha:0:8}, started $at): superseded by $url, which uploads its commits too"
		note "$run_url" "$sha" "$at" "cancelled by this guard: superseded by $url, which uploads its commits too"
	done < <(runs "$st")
done

# Concurrency may have made the kill before this job's query: name those
# too. A run cancelled since this one started has its updated_at after this
# run's created_at; only the ones the group cancelled are this one's.
while IFS=$'\t' read -r id run_url sha at; do
	[ -n "$id" ] || continue
	case $seen in *" $id "*) continue ;; esac
	if by_group "$id"; then
		note "$run_url" "$sha" "$at" "cancelled by the testflight group: superseded by $url, which uploads its commits too"
	else
		note "$run_url" "$sha" "$at" "cancelled since this run started, not by the testflight group (by hand?)"
	fi
done < <(gh api "$wf?status=cancelled&per_page=30" | jq -r --arg me "$me" --arg mine "$mine" '
  .workflow_runs[]
  | select(.id < ($me | tonumber) and .updated_at > $mine)
  | [.id, .html_url, .head_sha, .created_at] | @tsv')

if [ -n "$lines" ]; then
	{
		echo "### Superseded TestFlight runs"
		echo
		echo "A newer run cancels an older one in flight or queued: it uploads the older one's commits too."
		echo
		printf '%s' "$lines"
	} >> "$summary"
fi
echo "guard: uploading ${GITHUB_SHA:0:8}"
