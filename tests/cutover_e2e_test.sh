#!/usr/bin/env bash
# Opt-in: local Linux Docker only. Never publishes host ports or mounts host state.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
for cmd in docker go openssl ssh-keygen timeout; do command -v "$cmd" >/dev/null; done
umask 077
work=$(mktemp -d)
run=portloom-cutover-$(openssl rand -hex 6)
network=$run
image=$run:sshd
bind_image=internetsystemsconsortium/bind9:9.20@sha256:978201d91548ec63c3b1c1befd5e6a3c9fa127f79f9058fd82d8e3493fe90ce1
containers=()
network_created=false
image_created=false
cleanup() {
  local status=$? failed=0
  trap - EXIT
  set +e
  if ((status)); then
    # Only product/service logs, never config, authorization files or tokens.
    for name in "${containers[@]}"; do
      printf '\n--- %s ---\n' "$name" >&2
      if [[ $name == "$run-dns" ]]; then
        timeout 8s docker logs --tail 30 "$name" >&2
      else
        timeout 8s docker exec "$name" sh -c 'for f in /fixture/*.log; do [ ! -f "$f" ] || tail -n 20 "$f"; done' >&2
      fi
    done
  fi
  for name in "${containers[@]}"; do timeout 15s docker rm -fv "$name" >/dev/null || failed=1; done
  if $network_created; then timeout 15s docker network rm "$network" >/dev/null || failed=1; fi
  if $image_created; then timeout 15s docker image rm "$image" >/dev/null || failed=1; fi
  rm -rf -- "$work" || failed=1
  if ((status == 0 && failed)); then status=1; fi
  if ((status == 0)); then printf 'cutover_e2e=ok\n'; fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

arch=$(docker info --format '{{.Architecture}}')
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) printf 'Unsupported Docker architecture: %s\n' "$arch" >&2; exit 1 ;;
esac
build() { env GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -buildvcs=false -trimpath -o "$work/$1" "$2"; }
build server ./cmd/server
build agent ./cmd/agent
build fixture ./tests/fixtures/cutover
# DNS provisioning runs on the host; don't assume its architecture matches Docker.
go build -buildvcs=false -o "$work/seed" ./tests/fixtures/cutover
if [[ -n ${PORTLOOM_CUTOVER_SSHD_IMAGE:-} ]]; then
  image=$PORTLOOM_CUTOVER_SSHD_IMAGE
  docker image inspect "$image" >/dev/null
else
  mkdir "$work/build"
  cp Dockerfile.sshd "$work/build/Dockerfile"
  cp -R sshd "$work/build/sshd"
  timeout 240s docker build -q -t "$image" "$work/build" >/dev/null
  image_created=true
fi
docker image inspect "$bind_image" >/dev/null 2>&1 || timeout 120s docker pull "$bind_image" >/dev/null
network_args=(--internal --label portloom.fixture=cutover)
if [[ -n ${PORTLOOM_CUTOVER_SUBNET:-} ]]; then network_args+=(--subnet "$PORTLOOM_CUTOVER_SUBNET"); fi
if ! docker network create "${network_args[@]}" "$network" >/dev/null; then
  printf 'If Docker address pools are exhausted, set PORTLOOM_CUTOVER_SUBNET to an unused private subnet (/28 or larger).\n' >&2
  exit 1
fi
network_created=true

create() {
  local name=$1 base=$2 entrypoint=$3
  shift 3
  docker create --init --network "$network" --no-healthcheck --label portloom.fixture=cutover \
    --name "$name" --entrypoint "$entrypoint" "$base" "$@" >/dev/null
  containers+=("$name")
  docker start "$name" >/dev/null
}
for role in verifier source target source-agent target-agent; do
  create "$run-$role" "$image" /bin/sleep infinity
  docker exec "$run-$role" mkdir -m 755 /fixture
  docker cp "$work/fixture" "$run-$role:/fixture/check"
  docker exec "$run-$role" chmod 755 /fixture/check
done
create "$run-dns" "$bind_image" /bin/sh -ec '
  while [ ! -f /fixture/start ]; do sleep 0.1; done
  chown -R bind:bind /fixture
  exec /usr/sbin/named -u bind -g -n 1 -c /fixture/named.conf
'
ip() { docker inspect --format "{{(index .NetworkSettings.Networks \"$network\").IPAddress}}" "$run-$1"; }
source_ip=$(ip source)
target_ip=$(ip target)
verifier_ip=$(ip verifier)
dns_ip=$(ip dns)
[[ -n $source_ip && -n $target_ip && -n $verifier_ip && -n $dns_ip ]]

mkdir "$work/dns"
openssl rand -base64 32 > "$work/dns/tsig"
"$work/seed" seed-dns "$work/dns" "$source_ip"
docker cp "$work/dns" "$run-dns:/fixture"
docker exec "$run-dns" touch /fixture/start

# Only the management hostnames are fixed. The business names have no hosts
# entries; the verifier's ordinary resolver must query the private BIND zone.
for role in verifier source-agent target-agent; do
  docker exec "$run-$role" sh -ec '
    printf "%s source.cutover.test\n%s target.cutover.test\n" "$1" "$2" >>/etc/hosts
    printf "nameserver %s\noptions timeout:1 attempts:2\n" "$3" >/etc/resolv.conf
  ' sh "$source_ip" "$target_ip" "$dns_ip"
done
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -noenc -days 2 \
  -subj '/CN=PortLoom isolated integration CA' -keyout "$work/ca.key" -out "$work/ca.pem" 2>/dev/null
for id in source target; do
  mkdir "$work/$id"
  node=$run-$id
  agent=$run-$id-agent
  openssl rand -hex 32 > "$work/$id/admin"
  openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -noenc \
    -subj "/CN=$id.cutover.test" -keyout "$work/$id/leaf.key" -out "$work/$id/leaf.csr" 2>/dev/null
  printf 'basicConstraints=critical,CA:FALSE\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:%s.cutover.test,DNS:a.cutover.test,DNS:b.cutover.test,DNS:a-preview-%s.cutover.test,DNS:b-preview-%s.cutover.test\n' \
    "$id" "$id" "$id" > "$work/$id/leaf.ext"
  openssl x509 -req -days 2 -in "$work/$id/leaf.csr" -CA "$work/ca.pem" -CAkey "$work/ca.key" \
    -extfile "$work/$id/leaf.ext" -out "$work/$id/leaf.pem" 2>/dev/null
  ssh-keygen -q -t ed25519 -N '' -f "$work/$id/hostkey"
  ssh-keygen -q -t ed25519 -N '' -f "$work/$id/agentkey"
  read -r keytype publickey _ < "$work/$id/hostkey.pub"
  printf '[%s.cutover.test]:2222 %s %s\n' "$id" "$keytype" "$publickey" > "$work/$id/known_hosts"
  docker exec "$node" sh -ec 'install -d -m 0700 -o tunnel -g tunnel /fixture/server-data; install -d -m 0755 -o tunnel -g tunnel /auth /fixture/web'
  docker cp "$work/server" "$node:/fixture/server"
  docker cp "$work/$id/hostkey" "$node:/hostkeys/ssh_host_ed25519_key"
  docker cp "$work/$id/hostkey.pub" "$node:/hostkeys/ssh_host_ed25519_key.pub"
  docker cp "$work/$id/leaf.pem" "$node:/fixture/leaf.pem"
  docker cp "$work/$id/leaf.key" "$node:/fixture/leaf.key"
  docker cp "$work/$id/admin" "$node:/fixture/admin"
  docker cp "$work/$id/admin" "$run-verifier:/fixture/$id.token"
  docker exec "$node" chmod 755 /fixture/server
  docker exec -d "$node" sh -c 'exec /usr/local/bin/portloom-sshd-entrypoint >/fixture/sshd.log 2>&1'
  docker exec -d "$node" sh -ec '
    export TM_ADMIN_TOKEN=$(cat /fixture/admin) TM_PUBLIC_HOST=$1.cutover.test TM_ENABLE_CUTOVER=true
    export TM_SERVING_NODE_ID=$1
    export TM_DATABASE_PATH=/fixture/server-data/portloom.db TM_WEB_DIR=/fixture/web TM_TCP_EDGE_BIND_HOST=off
    export TM_AUTHORIZED_KEYS_PATH=/auth/authorized_keys TM_SSH_HOST_PUBLIC_KEY_PATH=/hostkeys/ssh_host_ed25519_key.pub
    export TM_MANAGED_SSH_PORT=2222 TM_MANAGED_SSH_ISOLATED=true TM_PORT_RANGE_START=21000 TM_PORT_RANGE_END=21030
    exec runuser -u tunnel -- /fixture/server >/fixture/server.log 2>&1
  ' sh "$id"
  docker exec -d "$node" sh -c 'exec /fixture/check edge "$@" >/fixture/edge.log 2>&1' sh "$id" "$verifier_ip"
  docker cp "$work/agent" "$agent:/fixture/agent"
  docker cp "$work/ca.pem" "$agent:/fixture/ca.pem"
  docker cp "$work/$id/agentkey" "$agent:/fixture/id_ed25519"
  docker cp "$work/$id/agentkey.pub" "$agent:/fixture/id_ed25519.pub"
  docker cp "$work/$id/known_hosts" "$agent:/fixture/known_hosts"
  docker exec "$agent" sh -ec '
    install -d -m 0700 -o tunnel -g tunnel /fixture/agent-data
    chown tunnel:tunnel /fixture/id_ed25519 /fixture/id_ed25519.pub /fixture/known_hosts
    chmod 755 /fixture/agent; chmod 644 /fixture/ca.pem; chmod 600 /fixture/id_ed25519 /fixture/known_hosts
  '
  docker exec -d "$agent" sh -c 'exec runuser -u tunnel -- /fixture/check origin "$1" >/fixture/origin.log 2>&1' sh "$id"
done

printf '%s\n' '{"profiles":{
  "source":{"url":"https://source.cutover.test","management_host":"source.cutover.test","token_file":"source.token","ca_file":"ca.pem"},
  "target":{"url":"https://target.cutover.test","management_host":"target.cutover.test","token_file":"target.token","ca_file":"ca.pem"}
}}' > "$work/profiles.json"
docker cp "$work/profiles.json" "$run-verifier:/fixture/profiles.json"
docker cp "$work/ca.pem" "$run-verifier:/fixture/ca.pem"
docker cp "$work/dns/tsig" "$run-verifier:/fixture/tsig"
docker exec "$run-verifier" mkdir -m 700 /fixture/journal
check() {
  timeout 100s docker exec -e "FIXTURE_DNS=$dns_ip:53" -e "FIXTURE_SOURCE=$source_ip" -e "FIXTURE_TARGET=$target_ip" \
    "$run-verifier" /fixture/check "$1"
}
check enroll
for id in source target; do
  docker cp "$run-verifier:/fixture/$id.enrollment" "$work/$id/enrollment"
  docker cp "$work/$id/enrollment" "$run-$id-agent:/fixture/enrollment"
  docker exec -d "$run-$id-agent" sh -ec '
    export TM_ENROLLMENT_TOKEN=$(cat /fixture/enrollment) SSL_CERT_FILE=/fixture/ca.pem
    export TM_SERVER_URL=https://$1.cutover.test TM_CLIENT_NAME=$1-agent TM_AGENT_STATE_PATH=/fixture/agent-data/state.json
    export TM_MANAGED_SSH_ISOLATED=true TM_SSH_HOST=$1.cutover.test TM_SSH_PORT=2222 TM_SSH_USER=tunnel
    export TM_SSH_IDENTITY_FILE=/fixture/id_ed25519 TM_SSH_PUBLIC_KEY_FILE=/fixture/id_ed25519.pub TM_SSH_KNOWN_HOSTS_FILE=/fixture/known_hosts
    export TM_POLL_INTERVAL=1s TM_HEALTH_TIMEOUT=1s TM_REQUEST_TIMEOUT=4s TM_MANAGED_SSH_READY_PATH=/fixture/agent-data/ssh.ready
    exec runuser -u tunnel -- /fixture/agent >/fixture/agent.log 2>&1
  ' sh "$id"
done
check prepare
check forward
# Separate process, same on-disk journal. No reconstructed in-memory state.
check rollback
