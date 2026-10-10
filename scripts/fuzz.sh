#!/usr/bin/env bash
# Runs every fuzz target of the module (Fuzz* in the _test.go files), each
# for a while, and prints a summary. The inputs that fail are kept by Go in
# <package>/testdata/fuzz/<target>/ as usual: commit them, they become seeds
# that go test runs every time.
#
# Usage: scripts/fuzz.sh [duration] [jobs] [regexp]
#   duration  how long each target runs (go test -fuzztime): 10m by default
#   jobs      how many targets run at once: 1 by default; the CPUs are
#             shared between them (go test -parallel)
#   regexp    only the targets whose "package.Target" matches (grep -E)
# Examples:
#   scripts/fuzz.sh                 every target for 10 minutes, one by one
#   scripts/fuzz.sh 60s 4           a minute each, four at a time
#   scripts/fuzz.sh 6h 4            a long run
#   scripts/fuzz.sh 5m 1 'tmpl\.'   the templates only
# FUZZ_LOGS names the directory of the logs (a new temporary one by default).
# FUZZ_MINIMIZE is the time to minimize an input (go test -fuzzminimizetime):
# 0 (none) by default. Go minimizes each new interesting input too, not only
# the failing ones, and a target with large inputs (disk images) then spends
# most of its time on that; set it ("30s") to get smaller failing inputs.
# The exit status is not 0 when a target fails.
set -uo pipefail

DURATION="${1:-10m}"
JOBS="${2:-1}"
FILTER="${3:-}"
MINIMIZE="${FUZZ_MINIMIZE:-0}"
cd "$(dirname "$0")/.."

CPUS=$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || echo 4)
PAR=$(( CPUS / JOBS ))
(( PAR < 1 )) && PAR=1
LOGS="${FUZZ_LOGS:-$(mktemp -d "${TMPDIR:-/tmp}/althex-fuzz.XXXXXX")}"
mkdir -p "$LOGS"

# The targets: go test -list prints the names, then "ok <package>"
mapfile -t TARGETS < <(go test -list '^Fuzz' ./... 2>&1 | awk '
	/^Fuzz/ { names[++n] = $1; next }
	/^ok / { for (i = 1; i <= n; i++) print $2 " " names[i]; n = 0; next }
	/^(FAIL|---|panic)/ { print "LISTFAIL " $0 > "/dev/stderr"; n = 0 }
')
MODULE=$(go list -m)
SELECTED=()
for t in "${TARGETS[@]}"; do
	pkg=${t% *} name=${t#* }
	short=${pkg#"$MODULE"/}
	if [[ -z "$FILTER" ]] || grep -Eq -- "$FILTER" <<<"${short##*/}.$name"; then
		SELECTED+=("$t")
	fi
done
if (( ${#SELECTED[@]} == 0 )); then
	echo "no fuzz targets" >&2
	exit 1
fi
echo "${#SELECTED[@]} targets, $DURATION each, $JOBS at a time with -parallel $PAR; logs in $LOGS"

# run <package> <target>: one target; its status and time go to <log>.status
run() {
	local pkg=$1 name=$2 short=${1#"$MODULE"/}
	local log="$LOGS/${short//\//_}.$name.log"
	local start=$SECONDS
	go test -run '^$' -fuzz "^${name}\$" -fuzztime "$DURATION" -fuzzminimizetime "$MINIMIZE" -parallel "$PAR" "./$short" >"$log" 2>&1
	local status=$?
	echo "$status $(( SECONDS - start ))" >"$log.status"
	if (( status == 0 )); then
		echo "ok    $short $name ($(( SECONDS - start ))s)"
	else
		echo "FAIL  $short $name ($(( SECONDS - start ))s): $log"
	fi
}

START=$SECONDS
running=0
for t in "${SELECTED[@]}"; do
	if (( running >= JOBS )); then
		wait -n
		running=$(( running - 1 ))
	fi
	run "${t% *}" "${t#* }" &
	running=$(( running + 1 ))
done
wait

# The summary
failed=0
printf '\n%-6s %-34s %-18s %8s %14s %10s\n' STATUS PACKAGE TARGET SECONDS EXECS CORPUS
for t in "${SELECTED[@]}"; do
	pkg=${t% *} name=${t#* } short=${pkg#"$MODULE"/}
	log="$LOGS/${short//\//_}.$name.log"
	read -r status secs <"$log.status" 2>/dev/null || { status=1; secs=0; }
	# The last progress line: "fuzz: elapsed: 1m0s, execs: 123 (2/sec), new interesting: 4 (total: 9)"
	last=$(grep -E '^fuzz: elapsed' "$log" | tail -n 1)
	execs=$(sed -nE 's/.*execs: ([0-9]+).*/\1/p' <<<"$last")
	corpus=$(sed -nE 's/.*\(total: ([0-9]+)\).*/\1/p' <<<"$last")
	word=ok
	if [[ "$status" != 0 ]]; then
		word=FAIL
		failed=$(( failed + 1 ))
	fi
	printf '%-6s %-34s %-18s %8s %14s %10s\n' "$word" "$short" "$name" "$secs" "${execs:--}" "${corpus:--}"
done
echo
echo "$(( SECONDS - START ))s in all, $failed of ${#SELECTED[@]} failed; logs in $LOGS"
if (( failed > 0 )); then
	echo "The failing inputs are in <package>/testdata/fuzz/<target>/; see the logs." >&2
	exit 1
fi
