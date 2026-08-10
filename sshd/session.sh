#!/bin/sh
set -eu

agent_id=${1:-}
bind_address=${2:-}
case "$agent_id" in *[!A-Za-z0-9_-]*|'') exit 64;; esac
case "$bind_address" in 127.*.*.*) ;; *) exit 64;; esac

# The command is forced by the authenticated Agent's authorized_keys entry.
# A unique loopback address is therefore both the takeover scope and the
# boundary that prevents one Agent from disturbing another Agent's session.
hex_address=$(printf '%s\n' "$bind_address" | awk -F. '{ printf "%02X%02X%02X%02X", $4, $3, $2, $1 }')
lock_dir=/run/portloom-agent-sessions
mkdir -p "$lock_dir"
lock_file=$lock_dir/$agent_id.lock

exec 9>"$lock_file"
flock 9

inodes=$(
  awk -v address="$hex_address" '
    NR > 1 && $2 ~ ("^" address ":") && $4 == "0A" { print $10 }
  ' /proc/net/tcp /proc/net/tcp6 2>/dev/null || true
)

ancestors=" $$ "
parent=$PPID
while [ "$parent" -gt 1 ] 2>/dev/null; do
  ancestors="$ancestors$parent "
  parent=$(awk '{ print $4 }' "/proc/$parent/stat" 2>/dev/null || printf '1')
done

for inode in $inodes; do
  for socket in /proc/[0-9]*/fd/*; do
    [ "$(readlink "$socket" 2>/dev/null || true)" = "socket:[$inode]" ] || continue
    pid=${socket#/proc/}
    pid=${pid%%/*}
    case "$ancestors" in *" $pid "*) continue;; esac
    kill -TERM "$pid" 2>/dev/null || true
  done
done

# Let sshd release the old listeners before the Agent installs new forwards.
attempt=0
while [ "$attempt" -lt 20 ]; do
  remaining=false
  for inode in $inodes; do
    for socket in /proc/[0-9]*/fd/*; do
      [ "$(readlink "$socket" 2>/dev/null || true)" = "socket:[$inode]" ] || continue
      pid=${socket#/proc/}
      pid=${pid%%/*}
      case "$ancestors" in *" $pid "*) continue;; esac
      remaining=true
    done
  done
  [ "$remaining" = true ] || break
  attempt=$((attempt + 1))
  sleep 0.05
done
flock -u 9

trap 'exit 0' HUP INT TERM
while :; do sleep 86400 & wait $!; done
