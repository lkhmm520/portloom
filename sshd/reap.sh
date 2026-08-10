#!/bin/sh
set -eu

agent_id=${1:-}; bind_address=${2:-}; keep_pid=${3:-}; ack=${4:-}
case "$agent_id" in *[!A-Za-z0-9_-]*|'') exit 64;; esac
case "$bind_address" in 127.*.*.*) ;; *) exit 64;; esac
case "$keep_pid" in *[!0-9]*|'') exit 64;; esac
case "$ack" in *[!A-Za-z0-9_.-]*|'') exit 64;; esac
ack_path=/run/portloom-agent-sessions/$ack
[ -f "$ack_path" ] && [ "$(stat -c %u "$ack_path")" = 65532 ] || exit 64

hex_address=$(printf '%s\n' "$bind_address" | awk -F. '{ printf "%02X%02X%02X%02X", $4, $3, $2, $1 }')
inodes=$(awk -v address="$hex_address" 'NR > 1 && $2 ~ ("^" address ":") && $4 == "0A" { print $10 }' /proc/net/tcp /proc/net/tcp6 2>/dev/null || true)
ancestors=" $keep_pid "
parent=$keep_pid
while [ "$parent" -gt 1 ] 2>/dev/null; do
  parent=$(awk '{ print $4 }' "/proc/$parent/stat" 2>/dev/null || printf '1')
  ancestors="$ancestors$parent "
done
for inode in $inodes; do
  for socket in /proc/[0-9]*/fd/*; do
    [ "$(readlink "$socket" 2>/dev/null || true)" = "socket:[$inode]" ] || continue
    pid=${socket#/proc/}; pid=${pid%%/*}
    case "$ancestors" in *" $pid "*) continue;; esac
    kill -TERM "$pid" 2>/dev/null || true
  done
done
printf 'done\n' > "$ack_path"
