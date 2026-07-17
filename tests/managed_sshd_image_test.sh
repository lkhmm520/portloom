#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
container=portloom-managed-sshd-test-$$
auth_volume=portloom-managed-sshd-auth-$$
hostkey_volume=portloom-managed-sshd-hostkeys-$$
ssh_pid=""; http_pid=""
cleanup() {
  [ -z "$ssh_pid" ] || kill "$ssh_pid" >/dev/null 2>&1 || true
  [ -z "$http_pid" ] || kill "$http_pid" >/dev/null 2>&1 || true
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker volume rm -f "$auth_volume" "$hostkey_volume" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT
start_sshd() {
  docker run -d --name "$container" --network host \
    --read-only --tmpfs /run:size=8m,mode=0755 --tmpfs /tmp:size=8m,mode=1777 \
    --security-opt no-new-privileges:true --cap-drop ALL \
    --cap-add SETUID --cap-add SETGID --cap-add SYS_CHROOT \
    -e PORTLOOM_SSH_PORT="$ssh_port" -v "$hostkey_volume:/hostkeys" -v "$auth_volume:/auth:ro" \
    portloom-sshd:test >/dev/null
}
wait_sshd() {
  for _ in $(seq 1 100); do
    docker logs "$container" 2>&1 | grep -q 'Server listening' && return 0
    [ "$(docker inspect -f '{{.State.Running}}' "$container")" = true ] || break
    sleep 0.1
  done
  docker logs "$container" >&2
  return 1
}
read -r ssh_port local_port remote_port forbidden_port < <(python3 -c 'import socket; sockets=[socket.socket() for _ in range(4)]; [s.bind(("127.0.0.1",0)) for s in sockets]; print(*(s.getsockname()[1] for s in sockets))')
own_bind=127.23.45.67
other_bind=127.23.45.68
mkdir -p "$tmp/client"
docker volume create "$auth_volume" >/dev/null
docker volume create "$hostkey_volume" >/dev/null
ssh-keygen -q -t ed25519 -N '' -f "$tmp/client/id_ed25519"
pub=$(cut -d ' ' -f 1-2 "$tmp/client/id_ed25519.pub")
auth_line="no-agent-forwarding,no-X11-forwarding,no-pty,no-user-rc,permitlisten=\"$own_bind:*\" $pub portloom-agent:test"
docker run --rm -e AUTH_LINE="$auth_line" -v "$auth_volume:/auth" debian:bookworm-slim sh -c \
  'umask 077; printf "%s\n" "$AUTH_LINE" > /auth/authorized_keys; chown -R 65532:65532 /auth; chmod 700 /auth; chmod 600 /auth/authorized_keys'
docker build -f "$repo/Dockerfile.sshd" -t portloom-sshd:test "$repo" >/dev/null
start_sshd
wait_sshd
CONTAINER="$container" python3 - <<'PY'
import json, os, subprocess
cfg = json.loads(subprocess.check_output(["docker", "inspect", os.environ["CONTAINER"]]))[0]["HostConfig"]
assert cfg["ReadonlyRootfs"] is True, cfg
assert cfg["CapDrop"] == ["ALL"], cfg["CapDrop"]
assert set(cfg["CapAdd"]) == {"SETUID", "SETGID", "SYS_CHROOT"}, cfg["CapAdd"]
assert "no-new-privileges:true" in cfg["SecurityOpt"], cfg["SecurityOpt"]
PY
docker run --rm -v "$hostkey_volume:/hostkeys:ro" debian:bookworm-slim \
  cat /hostkeys/ssh_host_ed25519_key.pub > "$tmp/client/host_key.pub"
host_key=$(cut -d ' ' -f 1-2 "$tmp/client/host_key.pub")
printf '[127.0.0.1]:%s %s\n' "$ssh_port" "$host_key" > "$tmp/client/known_hosts"
python3 -m http.server "$local_port" --bind 127.0.0.1 --directory "$tmp/client" >/dev/null 2>&1 & http_pid=$!
ssh -N -p "$ssh_port" -i "$tmp/client/id_ed25519" -o BatchMode=yes -o StrictHostKeyChecking=yes \
  -o UserKnownHostsFile="$tmp/client/known_hosts" -o ExitOnForwardFailure=yes \
  -R "$own_bind:$remote_port:127.0.0.1:$local_port" tunnel@127.0.0.1 >"$tmp/client/ssh.log" 2>&1 & ssh_pid=$!
for _ in $(seq 1 100); do
  if curl --noproxy '*' -fsS "http://$own_bind:$remote_port/" >/dev/null 2>&1; then break; fi
  if ! kill -0 "$ssh_pid" >/dev/null 2>&1; then
    wait "$ssh_pid" || status=$?; cat "$tmp/client/ssh.log" >&2; docker logs "$container" >&2
    exit "${status:-1}"
  fi
  sleep 0.1
done
curl --noproxy '*' -fsS "http://$own_bind:$remote_port/" >/dev/null
if timeout 8 ssh -N -p "$ssh_port" -i "$tmp/client/id_ed25519" -o BatchMode=yes -o StrictHostKeyChecking=yes \
  -o UserKnownHostsFile="$tmp/client/known_hosts" -o ExitOnForwardFailure=yes \
  -R "$other_bind:$forbidden_port:127.0.0.1:$local_port" tunnel@127.0.0.1 >/dev/null 2>&1; then
  echo 'agent key bound another agent loopback address' >&2; exit 1
fi
if ssh -p "$ssh_port" -i "$tmp/client/id_ed25519" -o BatchMode=yes -o StrictHostKeyChecking=yes \
  -o UserKnownHostsFile="$tmp/client/known_hosts" tunnel@127.0.0.1 true >/dev/null 2>&1; then
  echo 'interactive command unexpectedly succeeded' >&2; exit 1
fi
first=$(docker run --rm -v "$hostkey_volume:/hostkeys:ro" debian:bookworm-slim sha256sum /hostkeys/ssh_host_ed25519_key.pub | cut -d ' ' -f1)
docker rm -f "$container" >/dev/null
start_sshd
wait_sshd
second=$(docker run --rm -v "$hostkey_volume:/hostkeys:ro" debian:bookworm-slim sha256sum /hostkeys/ssh_host_ed25519_key.pub | cut -d ' ' -f1)
test "$first" = "$second"
echo 'managed_sshd_e2e=ok'
