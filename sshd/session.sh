#!/bin/sh
set -eu

agent_id=${1:-}
bind_address=${2:-}
case "$agent_id" in *[!A-Za-z0-9_-]*|'') exit 64;; esac
case "$bind_address" in 127.*.*.*) ;; *) exit 64;; esac

# The command is forced by the authenticated Agent's authorized_keys entry.
# A unique loopback address is therefore both the takeover scope and the
# boundary that prevents one Agent from disturbing another Agent's session.
lock_dir=/run/portloom-agent-sessions
mkdir -p "$lock_dir"
lock_file=$lock_dir/$agent_id.lock

exec 9>"$lock_file"
flock 9

nonce=$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')
ack=$agent_id.$$.${nonce}.ack
ack_path=/run/portloom-reaper-acks/$ack
printf '%s %s %s %s\n' "$agent_id" "$bind_address" "$PPID" "$ack" > /run/portloom-reaper.fifo
attempt=0
while [ "$attempt" -lt 20 ]; do
  [ "$(cat "$ack_path" 2>/dev/null || true)" = done ] && break
  attempt=$((attempt + 1))
  sleep 0.05
done
result=$(cat "$ack_path" 2>/dev/null || true)
flock -u 9
[ "$result" = done ] || exit 1

trap 'exit 0' HUP INT TERM
while :; do sleep 86400 & wait $!; done
