#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
server="$repo/docs/public/install-server.sh"
agent="$repo/docs/public/install-agent.sh"
token=$(printf 'a%.0s' {1..64})
base_agent=(--server-url https://loom.example.com --name nas --token "$token" --ssh-host vps.example.com --ssh-port 2222 --ssh-host-key 'ssh-ed25519 AAAA')

fail() { echo "$*" >&2; exit 1; }
expect_reject() {
  local expected=$1; shift
  local output status=0
  output=$("$@" 2>&1) || status=$?
  [ "$status" -ne 0 ] || fail "unexpectedly accepted: $*"
  printf '%s' "$output" | grep -Fq -- "$expected" || fail "wrong rejection for $*: $output"
}
env_value() {
  local file=$1 wanted=$2 key value
  while IFS='=' read -r key value; do
    [ "$key" = "$wanted" ] && { printf '%s' "$value"; return 0; }
  done < "$file"
  return 1
}

bash -n "$server"; bash -n "$agent"
"$server" --help | grep -q 'public Docker host'
"$agent" --help | grep -q 'internal Docker host'
expected_tls_ask="127.0.0.1:\$tls_ask_port/api/v1/tls/allow"
grep -Fq "$expected_tls_ask" "$server"
grep -q 'TM_MANAGED_SSH_ISOLATED: "true"' "$server"
grep -q 'TM_MANAGED_SSH_ISOLATED=true' "$agent"

args=("${base_agent[@]}"); args[1]=http://remote.example.com
expect_reject 'invalid --server-url' "$agent" "${args[@]}"
for bad_url in \
  'https://user@loom.example.com' \
  'https://loom.example.com/path' \
  'https://loom.example.com?x=1' \
  'https://loom.example.com#fragment' \
  $'https://loom.example.com\nTM_ALLOW_INSECURE_HTTP=true' \
  $'https://loom.example.com\rTM_ALLOW_INSECURE_HTTP=true'; do
  args=("${base_agent[@]}"); args[1]=$bad_url
  expect_reject 'invalid --server-url' "$agent" "${args[@]}"
done
args=("${base_agent[@]}"); args[7]=$'vps.example.com\nTM_SSH_PORT=22'
expect_reject 'invalid SSH host' "$agent" "${args[@]}"
expect_reject 'invalid --home' "$agent" "${base_agent[@]}" --home $'/tmp/agent\nTM_ENROLLMENT_TOKEN=stolen'
expect_reject 'invalid --version' "$agent" "${base_agent[@]}" --version $'latest\nTM_ALLOW_INSECURE_HTTP=true'
expect_reject 'unsafe or empty Agent name' "$agent" --server-url https://loom.example.com --name 'bad;name' --token "$token" \
  --ssh-host vps.example.com --ssh-port 2222 --ssh-host-key 'ssh-ed25519 AAAA'

for spec in '--web-port 80' '--web-port 443' '--web-port 8081' '--web-port 8082' '--ssh-port 80' '--ssh-port 443' '--ssh-port 8081' '--ssh-port 8082' '--web-port 2222 --ssh-port 2222'; do
  read -r -a port_args <<< "$spec"
  expect_reject 'port conflict' "$server" --domain loom.example.com "${port_args[@]}"
done

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"
cat > "$tmp/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [ "${FAKE_ASSERT_NO_LOCK_FD:-}" = 1 ] && [ -e /proc/$$/fd/9 ]; then
  printf 'installer lock leaked into Docker child: %q ' "$@" >&2; printf '\n' >&2
  exit 42
fi
printf '%q ' "$@" >> "${FAKE_DOCKER_LOG:?}"
printf '\n' >> "$FAKE_DOCKER_LOG"
if [ "${1:-}" = compose ] && [ "${2:-}" = version ]; then exit 0; fi
if [ "${1:-}" = compose ]; then
  if [[ " $* " == *' up '* ]] && [ -n "${FAKE_AGENT_READY:-}" ]; then
    mkdir -p "$PWD/data"
    printf '{}\n' > "$PWD/data/agent.json"
    current_nonce=$(awk -F= '$1=="TM_MANAGED_SSH_READY_NONCE" {print $2}' "$PWD/.env")
    [ -z "${FAKE_NONCE_LOG:-}" ] || printf '%s\n' "$current_nonce" >> "$FAKE_NONCE_LOG"
    if [ "$FAKE_AGENT_READY" = stale ]; then
      head -n 1 "$FAKE_NONCE_LOG" > "$PWD/data/managed-ssh.ready"
    else
      printf '%s\n' "$current_nonce" > "$PWD/data/managed-ssh.ready"
    fi
  fi
  exit 0
fi
[ "${1:-}" = pull ] && exit 0
[ "${1:-}" = run ] || exit 1
host_data=''; host_auth=''; host_server_data=''; entrypoint=''; host_label=''; host_key=''
for ((i=1; i<=$#; i++)); do
  arg=${!i}
  if [ "$arg" = -v ]; then
    ((i++)); volume=${!i}
    case "$volume" in
      *:/data|*:/data:ro) host_data=${volume%%:/data*};;
      *:/ssh-auth) host_auth=${volume%%:/ssh-auth};;
      *:/server-data) host_server_data=${volume%%:/server-data};;
    esac
  elif [ "$arg" = --entrypoint ]; then
    ((i++)); entrypoint=${!i}
  elif [ "$arg" = -e ]; then
    ((i++)); assignment=${!i}
    case "$assignment" in HOST_LABEL=*) host_label=${assignment#HOST_LABEL=};; HOST_KEY=*) host_key=${assignment#HOST_KEY=};; esac
  fi
done
case "$entrypoint" in
  /bin/cat)
    [ -f "$host_data/managed-ssh.ready" ] && cat "$host_data/managed-ssh.ready"
    ;;
  /usr/bin/test)
    test_flag=${@: -2:1}; test_path=${@: -1}
    case "$test_path" in
      /data/managed-ssh.ready) candidate="$host_data/managed-ssh.ready";;
      /data/agent.json) candidate="$host_data/agent.json";;
      /data/agent.json.pending) candidate="$host_data/agent.json.pending";;
      /data/ssh/id_ed25519) candidate="$host_data/ssh/id_ed25519";;
      /data/ssh/id_ed25519.pub) candidate="$host_data/ssh/id_ed25519.pub";;
      *) exit 1;;
    esac
    case "$test_flag" in -L) [ -L "$candidate" ];; -e) [ -e "$candidate" ];; -s) [ -s "$candidate" ];; -f) [ -f "$candidate" ];; *) exit 1;; esac
    ;;
  /bin/sh)
    if [[ "$*" == *'ssh-keygen -y'* ]]; then
      umask 077
      ssh-keygen -y -f "$host_data/ssh/id_ed25519" > "$host_data/ssh/id_ed25519.pub"
    elif [ -n "$host_auth" ]; then
      mkdir -p "$host_auth" "$host_server_data"
      touch "$host_auth/authorized_keys"
      chmod 700 "$host_auth" "$host_server_data"
      chmod 600 "$host_auth/authorized_keys"
    elif [ -n "$host_data" ]; then
      umask 077
      mkdir -p "$host_data/ssh"
      printf '%s %s\n' "$host_label" "$host_key" > "$host_data/ssh/known_hosts"
    fi
    ;;
  /bin/rm)
    if [[ " $* " == *' /data/managed-ssh.ready'* ]]; then rm -f "$host_data/managed-ssh.ready"; fi
    if [[ " $* " == *' /data/ssh/id_ed25519.pub'* ]]; then rm -f "$host_data/ssh/id_ed25519.pub"; fi
    ;;
  /usr/bin/ssh-keygen)
    mkdir -p "$host_data/ssh"
    ssh-keygen -q -t ed25519 -N '' -f "$host_data/ssh/id_ed25519"
    ;;
esac
EOF
chmod 755 "$tmp/bin/docker"
export PATH="$tmp/bin:$PATH" FAKE_DOCKER_LOG="$tmp/docker.log" FAKE_NONCE_LOG="$tmp/nonces.log" FAKE_ASSERT_NO_LOCK_FD=1
: > "$FAKE_DOCKER_LOG"; : > "$FAKE_NONCE_LOG"

mkdir -p "$tmp/locked-server"
exec 7>"$tmp/locked-server/.install.lock"
flock -n 7
expect_reject 'another Server installation is already running' env PORTLOOM_HOME="$tmp/locked-server" "$server" --domain loom.example.com
flock -u 7
exec 7>&-

PORTLOOM_HOME="$tmp/server-home" "$server" --domain loom.example.com >/dev/null
admin_before=$(env_value "$tmp/server-home/.env" TM_ADMIN_TOKEN)
tls_before=$(env_value "$tmp/server-home/.env" PORTLOOM_TLS_ASK_TOKEN)
printf 'existing-agent-key\n' > "$tmp/server-home/ssh-auth/authorized_keys"
PORTLOOM_HOME="$tmp/server-home" "$server" --domain loom.example.com >/dev/null
admin_after=$(env_value "$tmp/server-home/.env" TM_ADMIN_TOKEN)
tls_after=$(env_value "$tmp/server-home/.env" PORTLOOM_TLS_ASK_TOKEN)
[ "$admin_before" = "$admin_after" ] || fail 'server rerun replaced administrator token'
[ "$tls_before" = "$tls_after" ] || fail 'server rerun replaced TLS ask token'
grep -q '^existing-agent-key$' "$tmp/server-home/ssh-auth/authorized_keys" || fail 'server rerun replaced authorized_keys'
[ "$(stat -c %a "$tmp/server-home")" = 711 ] || fail 'server install root must be traverse-only for bind-mounted container UIDs'
for dir in "$tmp/server-home/server-data" "$tmp/server-home/ssh-auth" "$tmp/server-home/caddy-data" "$tmp/server-home/caddy-config"; do
  [ "$(stat -c %a "$dir")" = 700 ] || fail "sensitive directory is not mode 0700: $dir"
done
[ "$(stat -c %a "$tmp/server-home/ssh-hostkeys")" = 755 ] || fail 'host-key directory must expose the public key to the unprivileged Server'
grep -q 'on_demand_tls' "$tmp/server-home/Caddyfile"
grep -q '127.0.0.1:8082/api/v1/tls/allow' "$tmp/server-home/Caddyfile"

mkdir -p "$tmp/existing-agent/data"
printf '{"client_id":"old"}\n' > "$tmp/existing-agent/data/agent.json"
: > "$FAKE_DOCKER_LOG"
expect_reject 'credentials exist without .env and compose.yml' env PORTLOOM_HOME="$tmp/existing-agent" FAKE_AGENT_READY=1 "$agent" "${base_agent[@]}"
! grep -q ' up ' "$FAKE_DOCKER_LOG" || fail 'corrupt existing state started Docker services'
[ ! -e "$tmp/existing-agent/.env" ] || fail 'corrupt existing state caused a new token to be written'

mkdir -p "$tmp/recover-agent/data"
printf '{"client_id":"old"}\n' > "$tmp/recover-agent/data/agent.json"
cat > "$tmp/recover-agent/.env" <<EOF
PORTLOOM_AGENT_IMAGE=ghcr.io/lkhmm520/portloom-agent:latest
PORTLOOM_SSH_HOST_KEY_SHA256=$(printf '%s' 'ssh-ed25519 AAAA' | sha256sum | cut -d ' ' -f 1)
TM_SERVER_URL=https://loom.example.com
TM_CLIENT_NAME=nas
TM_ENROLLMENT_TOKEN=$token
TM_SSH_HOST=vps.example.com
TM_SSH_PORT=2222
EOF
printf 'services: {}\n' > "$tmp/recover-agent/compose.yml"
: > "$FAKE_DOCKER_LOG"
PORTLOOM_HOME="$tmp/recover-agent" FAKE_AGENT_READY=1 "$agent" "${base_agent[@]}" >/dev/null
[ -f "$tmp/recover-agent/data/managed-ssh.ready" ] || fail 'recovery did not observe managed SSH readiness'
! grep -q '^TM_ENROLLMENT_TOKEN=' "$tmp/recover-agent/.env" || fail 'recovery left the consumed token in .env'
grep -q ' up ' "$FAKE_DOCKER_LOG" || fail 'recovery did not start the existing Compose service'
[ "$(grep -c -- '--entrypoint /bin/rm .*managed-ssh.ready' "$FAKE_DOCKER_LOG")" -ge 2 ] || fail 'recovery did not clear readiness before both restarts'
[ "$(grep -c -- '--force-recreate' "$FAKE_DOCKER_LOG")" -ge 2 ] || fail 'recovery did not verify both credential-bearing and token-free restarts'
grep -q '^\[vps.example.com\]:2222 ssh-ed25519 AAAA$' "$tmp/recover-agent/data/ssh/known_hosts" || fail 'recovery did not apply the supplied SSH host key'
ready_nonce=$(env_value "$tmp/recover-agent/.env" TM_MANAGED_SSH_READY_NONCE)
[ "$(tr -d '\n' < "$tmp/recover-agent/data/managed-ssh.ready")" = "$ready_nonce" ] || fail 'recovery accepted a marker from another container generation'
[ "$(sort -u "$FAKE_NONCE_LOG" | wc -l)" -eq "$(wc -l < "$FAKE_NONCE_LOG")" ] || fail 'a readiness nonce was reused across container generations'
old_nonce=$(head -n 1 "$FAKE_NONCE_LOG")
expect_reject 'has not established managed SSH' env PORTLOOM_HOME="$tmp/recover-agent" FAKE_AGENT_READY=stale PORTLOOM_READY_ATTEMPTS=1 "$agent" "${base_agent[@]}"
[ "$(tr -d '\n' < "$tmp/recover-agent/data/managed-ssh.ready")" = "$old_nonce" ] || fail 'old-generation nonce injection was not exercised'

mkdir -p "$tmp/mismatch-agent/data"
printf '{"client_id":"old"}\n' > "$tmp/mismatch-agent/data/agent.json"
cp "$tmp/recover-agent/.env" "$tmp/mismatch-agent/.env"
printf 'services: {}\n' > "$tmp/mismatch-agent/compose.yml"
expect_reject 'does not match this install command' env PORTLOOM_HOME="$tmp/mismatch-agent" "$agent" --server-url https://other.example.com --name nas --token "$token" --ssh-host vps.example.com --ssh-port 2222 --ssh-host-key 'ssh-ed25519 AAAA'
expect_reject 'does not match this install command' env PORTLOOM_HOME="$tmp/mismatch-agent" "$agent" --server-url https://loom.example.com --name nas --token "$token" --ssh-host vps.example.com --ssh-port 2222 --ssh-host-key 'ssh-ed25519 AAAB'

mkdir -p "$tmp/locked-agent"
exec 8>"$tmp/locked-agent/.install.lock"
flock -n 8
expect_reject 'another Agent installation is already running' env PORTLOOM_HOME="$tmp/locked-agent" "$agent" "${base_agent[@]}"
flock -u 8
exec 8>&-

mkdir -p "$tmp/rebuild-agent/data/ssh"
ssh-keygen -q -t ed25519 -N '' -f "$tmp/rebuild-agent/data/ssh/id_ed25519"
rm "$tmp/rebuild-agent/data/ssh/id_ed25519.pub"
: > "$FAKE_DOCKER_LOG"
PORTLOOM_HOME="$tmp/rebuild-agent" FAKE_AGENT_READY=1 "$agent" "${base_agent[@]}" >/dev/null
ssh-keygen -lf "$tmp/rebuild-agent/data/ssh/id_ed25519.pub" >/dev/null || fail 'missing public key was not rebuilt from private key'
grep -q 'ssh-keygen.*-y' "$FAKE_DOCKER_LOG" || fail 'ssh-keygen -y was not used to rebuild public key'
[ -f "$tmp/rebuild-agent/data/managed-ssh.ready" ] || fail 'managed SSH readiness marker was not observed'
! grep -q '^TM_ENROLLMENT_TOKEN=' "$tmp/rebuild-agent/.env" || fail 'one-time token remained after managed SSH readiness'

echo 'installer_contracts=ok'
