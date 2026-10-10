#!/usr/bin/env bash
# Measures the cold start (PERF-1: up to the window ready, at most 300 ms):
# the time from starting the binary to its first paint, N times.
#   scripts/startup-time.sh [BINARY] [N]
# Runs on the display there is, or on a virtual one (Xvfb) on Linux; the
# settings are a fresh temporary directory, as on a first start. Measure on
# an idle machine: other loads make the numbers meaningless.
set -euo pipefail
BIN="${1:-./althex}"
N="${2:-10}"
WORK="$(mktemp -d)"
xvfb=""
cleanup() { [ -n "$xvfb" ] && kill "$xvfb" 2>/dev/null; rm -rf "$WORK"; }
trap cleanup EXIT
if [ -z "${DISPLAY:-}" ] && [ "$(uname -s)" = Linux ]; then
  Xvfb :95 -screen 0 1920x1080x24 >/dev/null 2>&1 &
  xvfb=$!
  export DISPLAY=:95
  sleep 1
fi
times=()
for i in $(seq "$N"); do
  rm -rf "$WORK/home"
  mkdir -p "$WORK/home"
  start=$(date +%s%N)
  HOME="$WORK/home" ALTHEX_TRACE_START=1 "$BIN" >"$WORK/out" 2>&1 &
  pid=$!
  for _ in $(seq 300); do
    grep -q "first paint" "$WORK/out" 2>/dev/null && break
    sleep 0.01
  done
  kill "$pid" 2>/dev/null
  wait "$pid" 2>/dev/null || true
  painted=$(sed -n 's/.*first paint \([0-9]*\).*/\1/p' "$WORK/out")
  if [ -z "$painted" ]; then
    echo "run $i: no paint in 3 s"
    continue
  fi
  ms=$(( (painted - start) / 1000000 ))
  times+=("$ms")
  echo "run $i: ${ms} ms"
done
[ "${#times[@]}" -gt 0 ] || exit 1
sorted=($(printf '%s\n' "${times[@]}" | sort -n))
echo "min ${sorted[0]} ms, median ${sorted[$(( ${#sorted[@]} / 2 ))]} ms, max ${sorted[-1]} ms (limit 300 ms)"
