#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
server_image=${PORTLOOM_TEST_SERVER_IMAGE:-portloom-server:test}
agent_image=${PORTLOOM_TEST_AGENT_IMAGE:-portloom-agent:test}
sshd_image=${PORTLOOM_TEST_SSHD_IMAGE:-portloom-sshd:test}
suffix=$$
backend_pid=""
created_alias=false
visible_tmp=""
if [ -n "${PORTLOOM_HOST_WORKSPACE:-}" ]; then
  visible_tmp=$(mktemp -d "$PORTLOOM_HOST_WORKSPACE/.installers-e2e.XXXXXX")
  host_tmp=$visible_tmp
elif mount | grep -q '^zfsv3 on /nas '; then
  visible_tmp=$(mktemp -d /nas/workspace/.installers-e2e.XXXXXX)
  alias_root=/tmp/zfsv3/sata11/13965810120/data/workspace
  if [ ! -e "$alias_root" ]; then
    mkdir -p "${alias_root%/workspace}"
    ln -s /nas/workspace "$alias_root"
    created_alias=true
  fi
  host_tmp="$alias_root/${visible_tmp##*/}"
else
  visible_tmp=$(mktemp -d)
  host_tmp=$visible_tmp
fi
server_home="$host_tmp/server"
agent_home="$host_tmp/agent"
chmod 0711 "$visible_tmp"
cleanup() {
  if [ "${PORTLOOM_KEEP_TEST_TMP:-false}" = true ]; then
    printf 'kept installer E2E temp: visible=%s host=%s\n' "$visible_tmp" "$host_tmp" >&2
    return
  fi
  [ -z "$backend_pid" ] || kill "$backend_pid" >/dev/null 2>&1 || true
  [ ! -f "$agent_home/compose.yml" ] || PORTLOOM_AGENT_IMAGE="$agent_image" docker compose -p "portloom-installer-agent-$suffix" --env-file "$agent_home/.env" -f "$agent_home/compose.yml" down -v >/dev/null 2>&1 || true
  [ ! -f "$server_home/compose.yml" ] || env -u PORTLOOM_DOMAIN -u PORTLOOM_WEB_PORT -u PORTLOOM_SSH_PORT -u PORTLOOM_GATEWAY_PORT -u PORTLOOM_HTTP_PORT -u PORTLOOM_HTTPS_PORT -u TM_ADMIN_TOKEN PORTLOOM_SERVER_IMAGE="$server_image" PORTLOOM_SSHD_IMAGE="$sshd_image" docker compose -p "portloom-installer-server-$suffix" --env-file "$server_home/.env" -f "$server_home/compose.yml" down -v >/dev/null 2>&1 || true
  docker rm -f portloom-agent portloom-server portloom-sshd >/dev/null 2>&1 || true
  docker run --rm -v "$host_tmp:/work" debian:bookworm-slim chown -R "$(id -u):$(id -g)" /work >/dev/null 2>&1 || true
  rm -rf "$visible_tmp" >/dev/null 2>&1 || true
  if [ "$created_alias" = true ]; then rm -f "$alias_root" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT
read -r web_port ssh_port gateway_port backend_port edge_http_port edge_https_port < <(python3 -c 'import socket; s=[socket.socket() for _ in range(6)]; [x.bind(("127.0.0.1",0)) for x in s]; print(*(x.getsockname()[1] for x in s))')
domain=installer-flow.example.test
PORTLOOM_SERVER_IMAGE_OVERRIDE="$server_image" PORTLOOM_SSHD_IMAGE_OVERRIDE="$sshd_image" \
PORTLOOM_SKIP_PULL=true PORTLOOM_NO_START=true PORTLOOM_GATEWAY_PORT="$gateway_port" \
PORTLOOM_HTTP_PORT="$edge_http_port" PORTLOOM_HTTPS_PORT="$edge_https_port" \
  "$repo/docs/public/install-server.sh" --domain "$domain" --home "$server_home" --web-port "$web_port" --ssh-port "$ssh_port" >/dev/null
(cd "$server_home" && env -u PORTLOOM_DOMAIN -u PORTLOOM_WEB_PORT -u PORTLOOM_SSH_PORT -u PORTLOOM_GATEWAY_PORT -u PORTLOOM_HTTP_PORT -u PORTLOOM_HTTPS_PORT -u TM_ADMIN_TOKEN PORTLOOM_SERVER_IMAGE="$server_image" PORTLOOM_SSHD_IMAGE="$sshd_image" docker compose -p "portloom-installer-server-$suffix" --env-file .env -f compose.yml up -d sshd server)
base="http://127.0.0.1:$web_port"
for _ in $(seq 1 100); do curl --noproxy '*' -fsS "$base/healthz" >/dev/null 2>&1 && break; sleep 0.1; done
curl --noproxy '*' -fsS "$base/healthz" >/dev/null
admin_token=$(awk -F= '$1=="TM_ADMIN_TOKEN" {print $2}' "$server_home/.env")
issue=$(curl --noproxy '*' -fsS -X POST -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' -d '{"expires_in":"1h"}' "$base/api/v1/enrollment-tokens")
enroll_token=$(printf '%s' "$issue" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')
ssh_host_key=$(cut -d ' ' -f 1-2 "$server_home/ssh-hostkeys/ssh_host_ed25519_key.pub")
# A protected pending identity without installer metadata must never be overwritten.
orphan_home="$host_tmp/orphan-agent"
mkdir -p "$orphan_home/data"
docker run --rm --user 0:0 -v "$orphan_home/data:/data" --entrypoint /bin/sh "$agent_image" -c   'printf "{\"client_id\":\"pending\",\"token\":\"pending\"}\n" > /data/agent.json.pending; chown -R 65532:65532 /data; chmod 0700 /data'
status=0
orphan_output=$(PORTLOOM_AGENT_IMAGE_OVERRIDE="$agent_image" PORTLOOM_SKIP_PULL=true   "$repo/docs/public/install-agent.sh" --server-url "$base" --name orphan-nas --token "$enroll_token"   --ssh-host 127.0.0.1 --ssh-port "$ssh_port" --ssh-host-key "$ssh_host_key" --home "$orphan_home" 2>&1) || status=$?
if [ "$status" -eq 0 ]; then
  echo 'installer overwrote a protected orphan pending identity' >&2
  exit 1
fi
printf '%s' "$orphan_output" | grep -q 'credentials exist without .env and compose.yml' || {
  echo "wrong orphan identity rejection: $orphan_output" >&2
  exit 1
}
PORTLOOM_AGENT_IMAGE_OVERRIDE="$agent_image" PORTLOOM_SKIP_PULL=true \
  "$repo/docs/public/install-agent.sh" --server-url "$base" --name installer-nas --token "$enroll_token" \
  --ssh-host 127.0.0.1 --ssh-port "$ssh_port" --ssh-host-key "$ssh_host_key" --home "$agent_home" >/dev/null
first_ready_nonce=$(awk -F= '$1=="TM_MANAGED_SSH_READY_NONCE" {print $2}' "$agent_home/.env")
# The exact command must recover safely after its one-time token has already been consumed.
PORTLOOM_AGENT_IMAGE_OVERRIDE="$agent_image" PORTLOOM_SKIP_PULL=true \
  "$repo/docs/public/install-agent.sh" --server-url "$base" --name installer-nas --token "$enroll_token" \
  --ssh-host 127.0.0.1 --ssh-port "$ssh_port" --ssh-host-key "$ssh_host_key" --home "$agent_home" >/dev/null
ready_nonce=$(awk -F= '$1=="TM_MANAGED_SSH_READY_NONCE" {print $2}' "$agent_home/.env")
[ -n "$first_ready_nonce" ] && [ "$ready_nonce" != "$first_ready_nonce" ]
observed_ready=$(docker run --rm --user 65532:65532 -v "$agent_home/data:/data:ro" --entrypoint /bin/cat "$agent_image" /data/managed-ssh.ready)
[ -n "$ready_nonce" ] && [ "$observed_ready" = "$ready_nonce" ]
if grep -q '^TM_ENROLLMENT_TOKEN=' "$agent_home/.env"; then
  echo 'recovery left enrollment token in Agent environment' >&2
  exit 1
fi
clients=$(curl --noproxy '*' -fsS -H "Authorization: Bearer $admin_token" "$base/api/v1/clients")
agent_id=$(printf '%s' "$clients" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert len(d)==1; print(d[0]["id"])')
printf 'installer-generated-flow\n' > "$visible_tmp/backend.txt"
python3 -m http.server "$backend_port" --bind 127.0.0.1 --directory "$visible_tmp" >/dev/null 2>&1 & backend_pid=$!
payload=$(AGENT_ID="$agent_id" BACKEND_PORT="$backend_port" python3 -c 'import json,os; print(json.dumps({"client_id":os.environ["AGENT_ID"],"name":"installer-backend","protocol":"http","domain":"installer-route.test","local_host":"127.0.0.1","local_port":int(os.environ["BACKEND_PORT"]),"tunnel_group":"web","enabled":True}))')
curl --noproxy '*' -fsS -X POST -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' -d "$payload" "$base/api/v1/routes" >/dev/null
check_route() { curl --noproxy '*' -fsS -H 'Host: installer-route.test' "http://127.0.0.1:$gateway_port/backend.txt" 2>/dev/null | grep -q installer-generated-flow; }
for _ in $(seq 1 120); do check_route && break; sleep 0.25; done
check_route
CONTAINER=portloom-server python3 - <<'PY'
import json, os, subprocess
container = json.loads(subprocess.check_output(["docker", "inspect", os.environ["CONTAINER"]]))[0]
cfg = container["HostConfig"]
caps = {cap.removeprefix("CAP_") for cap in cfg["CapAdd"]}
assert caps == {"NET_BIND_SERVICE"}, cfg["CapAdd"]
assert cfg["CapDrop"] == ["ALL"], cfg["CapDrop"]
assert container["Config"]["User"] == "tunnel", container["Config"]["User"]
assert "no-new-privileges:true" not in (cfg["SecurityOpt"] or []), cfg["SecurityOpt"]
status = subprocess.check_output([
    "docker", "exec", os.environ["CONTAINER"], "/bin/sh", "-c",
    'while IFS= read -r line; do case "$line" in Uid:*|CapEff:*) echo "$line";; esac; done < /proc/1/status',
], text=True).splitlines()
uid = next(line.split()[1] for line in status if line.startswith("Uid:"))
cap_eff = int(next(line.split()[1] for line in status if line.startswith("CapEff:")), 16)
assert uid == "65532", uid
assert cap_eff == 1 << 10, hex(cap_eff)
PY
check_control_edge() { [ "$(curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' --resolve "$domain:$edge_http_port:127.0.0.1" "http://$domain:$edge_http_port/healthz")" = 308 ]; }
check_route_edge() { [ "$(curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' --resolve "installer-route.test:$edge_http_port:127.0.0.1" "http://installer-route.test:$edge_http_port/backend.txt")" = 308 ]; }
check_unknown_edge() { [ "$(curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' --resolve "unknown-route.test:$edge_http_port:127.0.0.1" "http://unknown-route.test:$edge_http_port/")" = 404 ]; }
check_control_edge
check_route_edge
check_unknown_edge
curl --noproxy '*' -sS -D - -o /dev/null --resolve "$domain:$edge_http_port:127.0.0.1" "http://$domain:$edge_http_port/healthz" | tr -d '\r' | grep -Fqi "Location: https://$domain:$edge_https_port/healthz"
curl --noproxy '*' -sS -D - -o /dev/null --resolve "installer-route.test:$edge_http_port:127.0.0.1" "http://installer-route.test:$edge_http_port/backend.txt" | tr -d '\r' | grep -Fqi "Location: https://installer-route.test:$edge_https_port/backend.txt"
docker restart portloom-agent >/dev/null
for _ in $(seq 1 160); do check_route && check_route_edge && break; sleep 0.25; done
check_route
check_route_edge
echo 'installers_generated_compose_e2e=ok'
