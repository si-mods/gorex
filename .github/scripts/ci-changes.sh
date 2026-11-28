#!/usr/bin/env bash
# Whether the change reaches past the iOS app. Reads BASE (the commit to
# diff against) and GITHUB_EVENT_NAME; writes code=true|false to GITHUB_OUTPUT.
#
# A change only to the Swift app (IOS_ONLY: its sources, tests, packages,
# scripts and project), docs, .md files or the TestFlight workflow runs only
# what-to-test: the TestFlight workflow builds the app when it reaches main.
# Everything else runs all checks, a new path too: ios/share and ios/tailnet
# are Go, and Go's tests and the web build read the files in READ_ELSEWHERE.
# An empty diff, a manual run, and a base that is not an ancestor (a new
# branch, a force-push) run all.
set -euo pipefail

IOS_ONLY='^(ios/(GoRex|GoRexTests|SherpaOnnx|scripts)/|ios/(project\.yml|\.gitignore)$|docs/|\.github/workflows/testflight\.yml$)|\.md$'
READ_ELSEWHERE='^ios/GoRex/(Rex/RexProtocol\.gen\.swift|ghostty-themes\.json)$'

code=true
if [ "$GITHUB_EVENT_NAME" != workflow_dispatch ] && git merge-base --is-ancestor "$BASE" HEAD 2>/dev/null; then
  files=$(git diff --name-only "$BASE" HEAD)
  if [ -n "$files" ] &&
    ! printf '%s\n' "$files" | grep -Ev "$IOS_ONLY" | grep -q . &&
    ! printf '%s\n' "$files" | grep -Eq "$READ_ELSEWHERE"; then
    code=false
  fi
fi
echo "code=$code" | tee -a "$GITHUB_OUTPUT"
