#!/bin/sh
set -eu

command=${2:-}
[ "${1:-}" = -c ] || exit 1
case "$command" in
  /usr/sbin/nologin) ;;
  "/usr/local/bin/portloom-ssh-session "*) ;;
  *) exit 1;;
esac

# Refuse client-supplied lookalike commands from legacy key lines. The command
# must be forced by the authorized_keys line that authenticated this session.
auth_fingerprint=$(awk '$1 == "publickey" { print $3; exit }' "${SSH_USER_AUTH:-/dev/null}")
[ -n "$auth_fingerprint" ] || exit 1
matched=false
while IFS= read -r line; do
  case "$line" in *"command=\"$command\""*) ;; *) continue;; esac
  key=$(printf '%s\n' "$line" | awk '{ for (i=1; i<NF; i++) if ($i == "ssh-ed25519") { print $i, $(i+1); exit } }')
  [ -n "$key" ] || continue
  fingerprint=$(printf '%s\n' "$key" | ssh-keygen -lf /dev/stdin 2>/dev/null | awk '{ print $2 }')
  if [ "$fingerprint" = "$auth_fingerprint" ]; then matched=true; break; fi
done < /auth/authorized_keys
[ "$matched" = true ] || exit 1

if [ "$command" = /usr/sbin/nologin ]; then exec /usr/sbin/nologin; fi
set -- $command
[ "$#" -eq 3 ] || exit 1
exec "$@"
