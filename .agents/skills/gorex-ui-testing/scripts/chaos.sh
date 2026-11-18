#!/bin/sh
# chaos.sh [OUT]: run the fault scenarios of chaos_test.go and write their
# screenshots to OUT (default /tmp/gorex-chaos) for a person or an agent
# to compare with the last run. docs/chaos.md lists the scenarios and the
# invariant each one attacks. Give the test names after OUT to run a part:
#   chaos.sh /tmp/shots TestChaosBurstOutput
set -eu
out=${1:-/tmp/gorex-chaos}
shift 2>/dev/null || true
run=${*:-'^TestChaos'}
mkdir -p "$out"
GOREX_SHOTS=$out go test . -run "$run" -count=1 -v
echo "screenshots in $out:"
ls "$out"
