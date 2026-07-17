#!/usr/bin/env bash
set -euo pipefail
suffix=$$
server=portloom-flow-server-$suffix
sshd=portloom-flow-sshd-$suffix
agent=portloom-flow-agent-$suffix
data_vol=portloom-flow-server-data-$suffix
auth_vol=portloom-flow-auth-$suffix
hostkey_vol=portloom-flow-hostkeys-$suffix
agent_vol=portloom-flow-agent-data-$suffix
backend_pid=""
server_image=${PORTLOOM_SERVER_IMAGE:-portloom-server:validation}
agent_image=${PORTLOOM_AGENT_IMAGE:-portloom-agent:validation}
sshd_image=${PORTLOOM_SSHD_IMAGE:-portloom-sshd:validation}
cleanup() {
  [ -z "$backend_pid" ] || kill "$backend_pid" >/dev/null 2>&1 || true
  rm -f "/tmp/portloom-flow-backend-$suffix.txt"
  docker rm -f "$agent" "$server" "$sshd" >/dev/null 2>&1 || true
  docker volume rm -f "$data_vol" "$auth_vol" "$hostkey_vol" "$agent_vol" >/dev/null 2>&1 || true
}
trap cleanup EXIT
read -r control_port gateway_port tls_ask_port ssh_port backend_port remote_port < <(python3 -c 'import socket; sockets=[socket.socket() for _ in range(6)]; [s.bind(("127.0.0.1",0)) for s in sockets]; print(*(s.getsockname()[1] for s in sockets))')
read -r admin_token tls_ask_token < <(python3 -c 'import secrets; print(secrets.token_hex(16), secrets.token_hex(16))')
for volume in "$data_vol" "$auth_vol" "$hostkey_vol" "$agent_vol"; do docker volume create "$volume" >/dev/null; done
docker run --rm --user 0:0 -v "$data_vol:/data" -v "$auth_vol:/auth" --entrypoint /bin/sh "$server_image" -c \
  'touch /auth/authorized_keys; chown -R 65532:65532 /data /auth; chmod 600 /auth/authorized_keys'
docker run --rm --user 0:0 -v "$agent_vol:/data" --entrypoint /bin/sh "$agent_image" -c \
  'mkdir -p /data/ssh; chown -R 65532:65532 /data'
docker run --rm --user 65532:65532 -v "$agent_vol:/data" --entrypoint /usr/bin/ssh-keygen "$agent_image" \
  -q -t ed25519 -a 64 -N '' -C portloom-flow-agent -f /data/ssh/id_ed25519
docker run -d --name "$sshd" --network host -e PORTLOOM_SSH_PORT="$ssh_port" \
  -v "$hostkey_vol:/hostkeys" -v "$auth_vol:/auth:ro" "$sshd_image" >/dev/null
for _ in $(seq 1 100); do docker logs "$sshd" 2>&1 | grep -q 'Server listening' && break; sleep 0.1; done
docker run --rm --user 65532:65532 -e SSH_PORT="$ssh_port" -v "$hostkey_vol:/hostkeys:ro" -v "$agent_vol:/data" \
  --entrypoint /bin/sh "$agent_image" -c \
  'key=$(cut -d " " -f 1-2 /hostkeys/ssh_host_ed25519_key.pub); printf "[127.0.0.1]:%s %s\n" "$SSH_PORT" "$key" > /data/ssh/known_hosts; chmod 600 /data/ssh/known_hosts'
docker run -d --name "$server" --network host \
  -e TM_LISTEN_ADDR="127.0.0.1:$control_port" -e TM_GATEWAY_ADDR="127.0.0.1:$gateway_port" \
  -e TM_DATABASE_PATH=/data/portloom.db -e TM_WEB_DIR=/app/web -e TM_ADMIN_TOKEN="$admin_token" \
  -e TM_AUTHORIZED_KEYS_PATH=/auth/authorized_keys -e TM_SSH_HOST_PUBLIC_KEY_PATH=/hostkeys/ssh_host_ed25519_key.pub \
  -e TM_MANAGED_SSH_PORT="$ssh_port" -e TM_MANAGED_SSH_ISOLATED=true -e TM_PORT_RANGE_START="$remote_port" -e TM_PORT_RANGE_END="$remote_port" \
  -e TM_PUBLIC_HOST=control.flow.test -e TM_TLS_ASK_TOKEN="$tls_ask_token" -e TM_TLS_ASK_ADDR="127.0.0.1:$tls_ask_port" \
  -v "$data_vol:/data" -v "$auth_vol:/auth" -v "$hostkey_vol:/hostkeys:ro" "$server_image" >/dev/null
base="http://127.0.0.1:$control_port"
for _ in $(seq 1 100); do curl --noproxy '*' -fsS "$base/healthz" >/dev/null 2>&1 && break; sleep 0.1; done
curl --noproxy '*' -fsS "$base/healthz" >/dev/null
[ "$(curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' "$base/api/v1/tls/allow?token=$tls_ask_token&domain=control.flow.test")" = 404 ]
[ "$(curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$tls_ask_port/api/v1/tls/allow?token=wrong&domain=control.flow.test")" = 401 ]
[ "$(curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$tls_ask_port/api/v1/tls/allow?token=$tls_ask_token&domain=control.flow.test")" = 200 ]
issue=$(curl --noproxy '*' -fsS -X POST -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' \
  -d '{"expires_in":"1h"}' "$base/api/v1/enrollment-tokens")
enroll_token=$(printf '%s' "$issue" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')
printf 'portloom-flow-backend\n' > /tmp/portloom-flow-backend-$suffix.txt
python3 -m http.server "$backend_port" --bind 127.0.0.1 --directory /tmp >/dev/null 2>&1 & backend_pid=$!
docker run -d --name "$agent" --network host \
  -e TM_SERVER_URL="$base" -e TM_ALLOW_INSECURE_HTTP=true -e TM_CLIENT_NAME=flow-nas -e TM_ENROLLMENT_TOKEN="$enroll_token" \
  -e TM_AGENT_STATE_PATH=/data/agent.json -e TM_POLL_INTERVAL=1s -e TM_HEALTH_TIMEOUT=1s -e TM_REQUEST_TIMEOUT=3s \
  -e TM_SSH_HOST=127.0.0.1 -e TM_SSH_PORT="$ssh_port" -e TM_SSH_USER=tunnel \
  -e TM_SSH_IDENTITY_FILE=/data/ssh/id_ed25519 -e TM_SSH_PUBLIC_KEY_FILE=/data/ssh/id_ed25519.pub \
  -e TM_SSH_KNOWN_HOSTS_FILE=/data/ssh/known_hosts -e TM_MANAGED_SSH_ISOLATED=true -v "$agent_vol:/data" "$agent_image" >/dev/null
clients='[]'
for _ in $(seq 1 120); do
  clients=$(curl --noproxy '*' -fsS -H "Authorization: Bearer $admin_token" "$base/api/v1/clients" 2>/dev/null || printf '[]')
  [ "$(printf '%s' "$clients" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))' 2>/dev/null || printf 0)" = 1 ] && break
  sleep 0.25
done
agent_id=$(printf '%s' "$clients" | python3 -c 'import json,sys; data=json.load(sys.stdin); assert len(data)==1; print(data[0]["id"])')
payload=$(AGENT_ID="$agent_id" BACKEND_PORT="$backend_port" python3 -c 'import json,os; print(json.dumps({"client_id":os.environ["AGENT_ID"],"name":"flow-backend","protocol":"http","domain":"flow.test","local_host":"127.0.0.1","local_port":int(os.environ["BACKEND_PORT"]),"tunnel_group":"web","enabled":True}))')
curl --noproxy '*' -fsS -X POST -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' -d "$payload" "$base/api/v1/routes" >/dev/null
check_route() { curl --noproxy '*' -fsS -H 'Host: flow.test' "http://127.0.0.1:$gateway_port/portloom-flow-backend-$suffix.txt" 2>/dev/null | grep -q portloom-flow-backend; }
for _ in $(seq 1 120); do check_route && break; sleep 0.25; done
check_route
docker restart "$sshd" >/dev/null
for _ in $(seq 1 160); do check_route && break; sleep 0.25; done
check_route
docker restart "$server" >/dev/null
for _ in $(seq 1 160); do check_route && break; sleep 0.25; done
check_route
echo 'two_host_flow_e2e=ok'
