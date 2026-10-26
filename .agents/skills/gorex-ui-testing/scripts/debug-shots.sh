#!/bin/sh
# debug-shots.sh OUT [LINE...]: run GoRex Dev in a fresh data dir with the
# GOREX_DEBUG hook, send it the hook's lines, wait for the last "shot" and
# copy the PNGs to OUT. Its server is shut down and its dir removed after.
#   debug-shots.sh /tmp/shots 'appearance dark' 'shot dark.png'
set -eu
out=$1; shift
dir=$(mktemp -d /tmp/gxt.XXXX)
mkdir -p "$out"
GOREX_DIR=$dir GOREX_DEBUG=$dir go tool mygo dev >"$dir/dev.log" 2>&1 &
dev=$!
i=0
until [ -S "$dir/server.sock" ]; do
	i=$((i + 1)); [ $i -gt 600 ] && { echo "the app did not start; see $dir/dev.log" >&2; exit 1; }
	sleep 0.2
done
sleep 2
last=""
for line in "$@"; do
	printf '%s\n' "$line" >>"$dir/do.tmp"
	case $line in shot*|sshot*) last=${line#* } ;; esac
done
mv "$dir/do.tmp" "$dir/do"
if [ -n "$last" ]; then
	i=0
	until [ -f "$dir/$last" ]; do
		i=$((i + 1)); [ $i -gt 150 ] && { echo "no $last" >&2; break; }
		sleep 0.2
	done
fi
cp "$dir"/*.png "$out"/ 2>/dev/null || true
python3 -I "$(dirname "$0")/rexctl.py" --sock "$dir/server.sock" shutdown || true
kill $dev 2>/dev/null || true
wait $dev 2>/dev/null || true
rm -rf "$dir"
ls "$out"
