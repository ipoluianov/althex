#!/bin/sh
# Runs before the package is removed (nfpm.yaml, deb and rpm): stops the
# running copies of /usr/bin/althex, or they keep running from the deleted
# binary and hold the instance lock, so a new install does not start.
# Copies installed elsewhere (e.g. ~/.altbins by linux-install.sh) are left alone.

# Only on removal: deb passes "remove", rpm the number of versions left ("0");
# on upgrade the running copy is left to the user
case "$1" in
  remove | 0) ;;
  *) exit 0 ;;
esac

BIN=/usr/bin/althex

pids=""
for exe in /proc/[0-9]*/exe; do
  if [ "$(readlink "$exe" 2>/dev/null)" = "$BIN" ]; then
    pid=${exe#/proc/}
    pids="$pids ${pid%/exe}"
  fi
done
[ -n "$pids" ] || exit 0

# Unsaved edits are kept in the journal and offered after the next start;
# SIGTERM loses only the window position (saved on close)
kill $pids 2>/dev/null
i=0
while [ $i -lt 30 ]; do
  alive=""
  for pid in $pids; do
    kill -0 "$pid" 2>/dev/null && alive="$alive $pid"
  done
  [ -n "$alive" ] || exit 0
  pids=$alive
  sleep 0.1
  i=$((i + 1))
done
kill -9 $pids 2>/dev/null
exit 0
