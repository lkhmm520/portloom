#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
suffix=$$
project=portloom-beginner-lifecycle-$suffix
server_image=${PORTLOOM_TEST_SERVER_IMAGE:-portloom-server:test}
sshd_image=${PORTLOOM_TEST_SSHD_IMAGE:-portloom-sshd:test}
created_alias=false

if [ -n "${PORTLOOM_HOST_WORKSPACE:-}" ]; then
  tmp=$(mktemp -d "$PORTLOOM_HOST_WORKSPACE/.beginner-compose.XXXXXX")
  host_tmp=$tmp
elif mount | grep -q '^zfsv3 on /nas '; then
  tmp=$(mktemp -d /nas/workspace/.beginner-compose.XXXXXX)
  alias_root=/tmp/zfsv3/sata11/13965810120/data/workspace
  if [ ! -e "$alias_root" ]; then
    mkdir -p "${alias_root%/workspace}"
    ln -s /nas/workspace "$alias_root"
    created_alias=true
  fi
  host_tmp="$alias_root/${tmp##*/}"
else
  tmp=$(mktemp -d)
  host_tmp=$tmp
fi
chmod 0711 "$tmp"

compose() {
  docker compose -p "$project" --project-directory "$tmp" -f "$tmp/compose.yml" "$@"
}
cleanup() {
  compose down >/dev/null 2>&1 || true
  local name owner
  for name in portloom-server portloom-sshd; do
    owner=$(docker inspect -f '{{ index .Config.Labels "com.docker.compose.project" }}' "$name" 2>/dev/null || true)
    if [ "$owner" = "$project" ]; then
      docker rm -f "$name" >/dev/null 2>&1 || true
    fi
  done
  docker run --rm -v "$host_tmp:/work" debian:bookworm-slim chown -R "$(id -u):$(id -g)" /work >/dev/null 2>&1 || true
  rm -rf "$tmp" >/dev/null 2>&1 || true
  if [ "$created_alias" = true ]; then rm -f "$alias_root" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT

for name in portloom-server portloom-sshd; do
  if docker inspect "$name" >/dev/null 2>&1; then
    echo "refusing to run: container name $name is already in use" >&2
    exit 1
  fi
done

cp "$repo/examples/compose.yml" "$tmp/compose.yml"
cp "$repo/examples/compose.env.example" "$tmp/.env"
python3 - "$tmp/compose.yml" "$server_image" "$sshd_image" "$host_tmp" <<'PY'
import pathlib, sys
path = pathlib.Path(sys.argv[1])
server_image, sshd_image, host_tmp = sys.argv[2:]
text = path.read_text()
text = text.replace("ghcr.io/lkhmm520/portloom-server:0.4.1", server_image)
text = text.replace("ghcr.io/lkhmm520/portloom-sshd:0.4.1", sshd_image)
text = text.replace("./data/server:/data", f"{host_tmp}/data/server:/data")
text = text.replace("./data/ssh-auth:/auth", f"{host_tmp}/data/ssh-auth:/auth")
text = text.replace("./data/ssh-auth:/ssh-auth", f"{host_tmp}/data/ssh-auth:/ssh-auth")
text = text.replace("./data/ssh-hostkeys:/hostkeys", f"{host_tmp}/data/ssh-hostkeys:/hostkeys")
text = text.replace("./data/ssh-hostkeys:/ssh-hostkeys", f"{host_tmp}/data/ssh-hostkeys:/ssh-hostkeys")
path.write_text(text)
PY

read -r web_port gateway_port ssh_port edge_http_port edge_https_port < <(python3 -c 'import socket; s=[socket.socket() for _ in range(5)]; [x.bind(("127.0.0.1",0)) for x in s]; print(*(x.getsockname()[1] for x in s))')
token=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
cat >> "$tmp/.env" <<EOF
TM_PUBLIC_HOST=beginner.example.test
TM_ADMIN_TOKEN=$token
TM_MANAGED_SSH_PORT=$ssh_port
TM_LISTEN_ADDR=127.0.0.1:$web_port
TM_HEALTHCHECK_URL=http://127.0.0.1:$web_port/healthz
TM_GATEWAY_ADDR=127.0.0.1:$gateway_port
TM_EDGE_HTTP_ADDR=127.0.0.1:$edge_http_port
TM_EDGE_HTTPS_ADDR=127.0.0.1:$edge_https_port
TM_TCP_EDGE_BIND_HOST=off
EOF
chmod 0600 "$tmp/.env"

PORTLOOM_SERVER_IMAGE=should-not-override TM_SERVER_DATA_DIR=/should-not-override TM_TLS_CACHE_DIR=/should-not-override \
  compose config --format json | \
  TOKEN="$token" EXPECTED_SERVER_IMAGE="$server_image" EXPECTED_DATA="$host_tmp/data/server" python3 -c '
import json, os, sys
cfg = json.load(sys.stdin)
server = cfg["services"]["server"]
assert server["environment"]["TM_ADMIN_TOKEN"] == os.environ["TOKEN"]
assert server["environment"]["TM_TLS_CACHE_DIR"] == "/data/certs"
assert server["image"] == os.environ["EXPECTED_SERVER_IMAGE"]
sources = {mount["source"] for mount in server["volumes"]}
assert os.environ["EXPECTED_DATA"] in sources, sources
'

wait_healthy() {
  local name=$1 status
  for _ in $(seq 1 200); do
    status=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$name" 2>/dev/null || true)
    [ "$status" = healthy ] && return 0
    [ "$status" != exited ] || { docker logs "$name" >&2; return 1; }
    sleep 0.1
  done
  docker logs "$name" >&2 || true
  return 1
}
check_state_init() {
  local container status
  container=$(compose ps -aq state-init)
  [ -n "$container" ]
  status=$(docker inspect -f '{{.State.Status}} {{.State.ExitCode}}' "$container")
  [ "$status" = "exited 0" ] || { docker logs "$container" >&2; return 1; }
}
check_running() {
  wait_healthy portloom-sshd
  wait_healthy portloom-server
  check_state_init
  curl --noproxy '*' -fsS "http://127.0.0.1:$web_port/healthz" >/dev/null
}

compose up -d
check_running
compose up -d
check_running
compose restart server sshd >/dev/null
check_running

hostkey_before=$(docker run --rm -v "$host_tmp/data/ssh-hostkeys:/keys:ro" debian:bookworm-slim sha256sum /keys/ssh_host_ed25519_key.pub | cut -d ' ' -f1)
compose down
compose up -d
check_running
hostkey_after=$(docker run --rm -v "$host_tmp/data/ssh-hostkeys:/keys:ro" debian:bookworm-slim sha256sum /keys/ssh_host_ed25519_key.pub | cut -d ' ' -f1)
[ "$hostkey_before" = "$hostkey_after" ]

docker run --rm -v "$host_tmp/data/server:/data:ro" -v "$host_tmp/data/ssh-auth:/auth:ro" debian:bookworm-slim sh -c '
  test -s /data/portloom.db
  test "$(stat -c %u:%g /data)" = 65532:65532
  test "$(stat -c %u:%g /data/certs)" = 65532:65532
  test "$(stat -c %a /data)" = 700
  test "$(stat -c %a /data/certs)" = 700
  test "$(stat -c %u:%g /auth/authorized_keys)" = 65532:65532
  test "$(stat -c %a /auth/authorized_keys)" = 600
'

echo 'beginner_compose_lifecycle=ok'
