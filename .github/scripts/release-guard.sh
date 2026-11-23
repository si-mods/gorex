#!/usr/bin/env bash
# At a release run's start, on the instabox runner (release.yml's guard
# job):
#
#  * for a full dispatch (TARGET all), cancels every older run of this
#    workflow still in flight or queued, and names it and the reason on
#    this run's page: the cancelled run's page cannot carry one. A newer
#    dispatch supersedes it — its apps and its draft would be of an older
#    main. Concurrency's cancel-in-progress makes the kill itself; the loop
#    is a net for a cancellation still propagating when this job starts.
#    A partial dispatch (unix, windows, web) cancels nothing: it would cut
#    the other platforms out of a full release, so the group makes it wait.
#  * deletes the draft a superseded run left of another version (0.54.0's,
#    once 0.55.0 supersedes it): no run finishes it any more. Only a draft,
#    only of a run this guard or the release group cancelled, and only
#    once that run has stopped, so that its upload cannot create it again.
#    A draft of this run's version stays: this run uploads to it.
#  * warns when main left this dispatch's commit while it queued: the apps
#    are of that commit, and so is the tag (draft-release.sh pins it), so
#    the release is whole, only older than main.
#
#   release-guard.sh
#
# DRY_RUN=1 prints the cancels and deletes instead of making them, and the
# summary to stdout when GITHUB_STEP_SUMMARY is unset. Run it at the
# repository root. Needs gh and jq; GITHUB_* of the Actions run, and TARGET.
set -euo pipefail

repo=$GITHUB_REPOSITORY
me=$GITHUB_RUN_ID
target=${TARGET:-all}
summary=${GITHUB_STEP_SUMMARY:-/dev/stdout}
url="$GITHUB_SERVER_URL/$repo/actions/runs/$me"
mine=$(gh api "repos/$repo/actions/runs/$me" --jq .created_at)
version=$(jq -r .version mygo.json)

wf="repos/$repo/actions/workflows/release.yml/runs"

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

# by_group id: the release group cancelled run id for a newer one. GitHub
# says so in an annotation on its jobs; a run cancelled by hand has none.
by_group() {
	local job
	for job in $(gh api "repos/$repo/actions/runs/$1/jobs" --jq '.jobs[].id'); do
		gh api "repos/$repo/check-runs/$job/annotations" --jq '.[].message' |
			grep -q 'higher priority waiting request' && return 0
	done
	return 1
}

# stopped id: waits up to a minute for run id to stop.
stopped() {
	local i
	for i in $(seq 20); do
		[ "$(gh api "repos/$repo/actions/runs/$1" --jq .status)" = completed ] && return 0
		[ "${DRY_RUN:-}" = 1 ] && return 0
		sleep 3
	done
	return 1
}

# drop_draft id sha: deletes the draft of run id's version, at commit sha,
# when it is another version than this run's. Prints what it did.
drop_draft() {
	local v draft
	v=$(gh api "repos/$repo/contents/mygo.json?ref=$2" --jq .content | base64 -d | jq -r .version) || v=
	if [ -z "$v" ]; then
		echo "its version is unknown; no draft deleted"
		return
	fi
	if [ "$v" = "$version" ]; then
		echo "its draft v$v is this run's, which uploads to it"
		return
	fi
	draft=$(gh release view "v$v" --repo "$repo" --json isDraft --jq .isDraft 2>/dev/null) || {
		echo "it left no v$v release"
		return
	}
	if [ "$draft" != true ]; then
		echo "v$v is published; kept"
		return
	fi
	if ! stopped "$1"; then
		echo "::warning::Release run $1 has not stopped; its draft v$v stays" >&2
		echo "it has not stopped; its draft v$v stays: delete it by hand"
		return
	fi
	act gh release delete "v$v" --repo "$repo" --yes
	echo "deleted its draft v$v"
}

lines= seen=" "
note() { # url sha at reason
	lines="$lines- $1 — sha \`${2:0:8}\`, dispatched $3 — $4"$'\n'
}

if [ "$target" = all ]; then
	# An id grows with the dispatch order, so the runs this one supersedes
	# are exactly the older ones: a newer full run never meets this job —
	# the group cancels this one instead.
	for st in in_progress queued waiting pending requested; do
		while IFS=$'\t' read -r id run_url sha at; do
			[ -n "$id" ] || continue
			seen="$seen$id "
			act gh api -X POST "repos/$repo/actions/runs/$id/cancel"
			echo "::warning::Cancelled release run $run_url (sha ${sha:0:8}, dispatched $at): superseded by a newer full dispatch $url"
			note "$run_url" "$sha" "$at" "cancelled by this guard: superseded by $url, a newer full dispatch; $(drop_draft "$id" "$sha")"
		done < <(runs "$st")
	done

	# Concurrency may have made the kill before this job's query: name those
	# too. A run cancelled since this dispatch has its updated_at after this
	# run's created_at; only the ones the group cancelled are this one's.
	while IFS=$'\t' read -r id run_url sha at; do
		[ -n "$id" ] || continue
		case $seen in *" $id "*) continue ;; esac
		if by_group "$id"; then
			note "$run_url" "$sha" "$at" "cancelled by the release group: superseded by $url, a newer full dispatch; $(drop_draft "$id" "$sha")"
		else
			note "$run_url" "$sha" "$at" "cancelled since this dispatch, not by the release group (by hand?); its draft is left alone"
		fi
	done < <(gh api "$wf?status=cancelled&per_page=30" | jq -r --arg me "$me" --arg mine "$mine" '
	  .workflow_runs[]
	  | select(.id < ($me | tonumber) and .updated_at > $mine)
	  | [.id, .html_url, .head_sha, .created_at] | @tsv')

	if [ -n "$lines" ]; then
		{
			echo "### Superseded release runs"
			echo
			echo "A newer full dispatch cancels an older release run in flight or queued."
			echo
			printf '%s' "$lines"
		} >> "$summary"
	fi
else
	echo "guard: a $target dispatch cancels no run; the release group queues it behind one in flight"
fi

head=$(gh api "repos/$repo/commits/$GITHUB_REF_NAME" --jq .sha)
if [ "$head" != "$GITHUB_SHA" ]; then
	{
		echo "### Older than main"
		echo
		echo "main moved to \`${head:0:8}\` since this dispatch of \`${GITHUB_SHA:0:8}\`. The apps and the tag are of \`${GITHUB_SHA:0:8}\`; dispatch again to ship main's head."
	} >> "$summary"
	echo "::warning::main is ${head:0:8}, this run builds ${GITHUB_SHA:0:8}: the release is of the older commit"
fi
echo "guard: building ${GITHUB_SHA:0:8} of v$version (target $target)"
