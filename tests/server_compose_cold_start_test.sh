#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
suffix=$$
project=portloom-cold-start-$suffix
export PORTLOOM_SERVER_IMAGE=${PORTLOOM_TEST_SERVER_IMAGE:-portloom-server:test}
export PORTLOOM_SSHD_IMAGE=${PORTLOOM_TEST_SSHD_IMAGE:-portloom-sshd:test}
if [ -n "${PORTLOOM_HOST_WORKSPACE:-}" ]; then
  tmp=$(mktemp -d "$PORTLOOM_HOST_WORKSPACE/.server-compose-cold.XXXXXX")
  host_tmp="$tmp"
elif mount | grep -q '^zfsv3 on /nas '; then
  tmp=$(mktemp -d /nas/workspace/.server-compose-cold.XXXXXX)
  host_tmp="/tmp/zfsv3/sata11/13965810120/data/workspace/${tmp##*/}"
else
  tmp=$(mktemp -d)
  host_tmp="$tmp"
fi
cleanup() {
  docker compose -p "$project" --env-file "$tmp/server.env" -f "$tmp/compose.yml" down -v >/dev/null 2>&1 || true
  docker rm -f portloom-sshd >/dev/null 2>&1 || true
  docker run --rm -v "$host_tmp:/work" debian:bookworm-slim chown -R "$(id -u):$(id -g)" /work >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT
cp "$repo/examples/docker-compose.server.yml" "$tmp/compose.yml"
cp "$repo/examples/server.env.example" "$tmp/server.env"
mkdir -p "$tmp/data/server" "$tmp/data/ssh-auth" "$tmp/data/ssh-hostkeys"
cat >> "$tmp/server.env" <<EOF
PORTLOOM_SERVER_IMAGE=${PORTLOOM_SERVER_IMAGE:-portloom-server:test}
PORTLOOM_SSHD_IMAGE=${PORTLOOM_SSHD_IMAGE:-portloom-sshd:test}
TM_SERVER_DATA_DIR=$host_tmp/data/server
TM_SSH_AUTH_DIR=$host_tmp/data/ssh-auth
TM_SSH_HOSTKEYS_DIR=$host_tmp/data/ssh-hostkeys
TM_MANAGED_SSH_PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
EOF
docker compose -p "$project" --env-file "$tmp/server.env" -f "$tmp/compose.yml" up -d sshd
for _ in $(seq 1 100); do
  status=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' portloom-sshd 2>/dev/null || true)
  [ "$status" = healthy ] && break
  [ "$status" != exited ] || { docker logs portloom-sshd >&2; exit 1; }
  sleep 0.1
done
[ "$(docker inspect -f '{{.State.Health.Status}}' portloom-sshd)" = healthy ]
docker run --rm -v "$host_tmp/data/ssh-auth:/auth:ro" -v "$host_tmp/data/ssh-hostkeys:/hostkeys:ro" debian:bookworm-slim sh -c \
  'test -f /auth/authorized_keys; test -s /hostkeys/ssh_host_ed25519_key.pub; test "$(stat -c %u:%g /auth/authorized_keys)" = 65532:65532; test "$(stat -c %a /auth)" = 700; test "$(stat -c %a /auth/authorized_keys)" = 600'
CONTAINER=portloom-sshd python3 - <<'PY'
import json, os, subprocess
cfg = json.loads(subprocess.check_output(["docker", "inspect", os.environ["CONTAINER"]]))[0]["HostConfig"]
assert cfg["ReadonlyRootfs"] is True, cfg
assert cfg["CapDrop"] == ["ALL"], cfg["CapDrop"]
caps = {cap.removeprefix("CAP_") for cap in cfg["CapAdd"]}
assert caps == {"SETUID", "SETGID", "SYS_CHROOT"}, cfg["CapAdd"]
assert "no-new-privileges:true" in cfg["SecurityOpt"], cfg["SecurityOpt"]
PY
echo 'server_compose_cold_start=ok'
