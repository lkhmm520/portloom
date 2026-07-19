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
grep -q 'TM_EDGE_HTTP_ADDR: :\${PORTLOOM_HTTP_PORT}' "$server"
grep -q 'TM_EDGE_HTTPS_ADDR: :\${PORTLOOM_HTTPS_PORT}' "$server"
grep -q 'TM_HEALTHCHECK_URL: http://127.0.0.1:\${PORTLOOM_WEB_PORT}/healthz' "$server"
grep -q 'cap_add: \[NET_BIND_SERVICE\]' "$server"
! grep -q 'cap_add:.*NET_BIND_SERVICE' "$repo/deploy/server/docker-compose.yml"
! grep -q 'cap_add:.*NET_BIND_SERVICE' "$repo/examples/docker-compose.server.yml"
! grep -q 'container_name: portloom-caddy' "$server"
grep -q 'TM_MANAGED_SSH_ISOLATED: "true"' "$server"
grep -q 'TM_MANAGED_SSH_ISOLATED=true' "$agent"
grep -q '/opt/bin/flock' "$agent" || fail 'Agent installer does not probe the QNAP Entware flock path'
grep -q '/opt/bin/flock' "$server" || fail 'Server installer does not probe the QNAP Entware flock path'

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

for bad_domain in 'console.example.com:443' 'console..example.com' '127.0.0.1' '127.1' '2130706433' 'localhost' '2001:db8::1' "$(printf 'a%.0s' {1..64}).example.com"; do
  expect_reject '--domain must be a valid DNS name' "$server" --domain "$bad_domain"
done

for spec in '--web-port 80' '--web-port 443' '--web-port 8081' '--ssh-port 80' '--ssh-port 443' '--ssh-port 8081' '--web-port 2222 --ssh-port 2222'; do
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
if [ -n "${FAKE_FAIL_DOCKER_MATCH:-}" ] && [[ " $* " == *"$FAKE_FAIL_DOCKER_MATCH"* ]]; then echo 'injected docker failure' >&2; exit 43; fi
if [ "${1:-}" = info ]; then exit 0; fi
if [ "${1:-}" = compose ] && [ "${2:-}" = version ]; then exit 0; fi
if [ "${1:-}" = image ] && [ "${2:-}" = inspect ]; then
  image=${@: -1}
  case "$image" in
    *portloom-server*) printf '%s\n' "${FAKE_SERVER_IMAGE_ID:-sha256:$(printf 'a%.0s' {1..64})}";;
    *portloom-agent*) printf '%s\n' "${FAKE_AGENT_IMAGE_ID:-sha256:$(printf 'b%.0s' {1..64})}";;
    *portloom-sshd*) printf '%s\n' "${FAKE_SSHD_IMAGE_ID:-sha256:$(printf 'c%.0s' {1..64})}";;
    sha256:*) printf '%s\n' "$image";;
    *) exit 1;;
  esac
  exit 0
fi
if [ "${1:-}" = inspect ]; then
  case "${@: -1}" in
    portloom-server) result=${FAKE_DOCKER_INSPECT_SERVER_RESULT:-};;
    portloom-sshd) result=${FAKE_DOCKER_INSPECT_SSHD_RESULT:-};;
    *) result=${FAKE_DOCKER_INSPECT_RESULT:-};;
  esac
  if [ -z "$result" ] && [ "${FAKE_DOCKER_INSPECT_AUTO_RUNNING:-}" = 1 ]; then
    case "${@: -1}" in
      portloom-server) result="${PORTLOOM_HOME}|server|sha256:$(printf 'a%.0s' {1..64})|true";;
      portloom-sshd) result="${PORTLOOM_HOME}|sshd|sha256:$(printf 'c%.0s' {1..64})|true";;
    esac
  fi
  [ -n "$result" ] || exit 1
  printf '%s\n' "$result"
  exit 0
fi
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
[ "${1:-}" = exec ] && exit 0
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
      mkdir -p "$host_auth" "$host_server_data/certs"
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
server_image_id=$(env_value "$tmp/server-home/.env" PORTLOOM_SERVER_IMAGE_ID)
sshd_image_id=$(env_value "$tmp/server-home/.env" PORTLOOM_SSHD_IMAGE_ID)
[[ "$server_image_id" =~ ^sha256:[0-9a-f]{64}$ ]] || fail 'fresh Server install did not persist the immutable Server image ID'
[[ "$sshd_image_id" =~ ^sha256:[0-9a-f]{64}$ ]] || fail 'fresh Server install did not persist the immutable SSHD image ID'
printf 'existing-agent-key\n' > "$tmp/server-home/ssh-auth/authorized_keys"
: > "$FAKE_DOCKER_LOG"
PORTLOOM_HOME="$tmp/server-home" FAKE_SERVER_IMAGE_ID="sha256:$(printf 'd%.0s' {1..64})" FAKE_SSHD_IMAGE_ID="sha256:$(printf 'e%.0s' {1..64})" "$server" --domain loom.example.com >/dev/null
! grep -q '^pull ' "$FAKE_DOCKER_LOG" || fail 'idempotent rerun pulled a mutable image reference'
grep -q '^run --pull=never --rm ' "$FAKE_DOCKER_LOG" || fail 'server initialization allowed an implicit mutable-tag pull'
grep -q 'compose .*up -d --remove-orphans --pull never' "$FAKE_DOCKER_LOG" || fail 'server activation allowed an implicit image pull'
grep -q -- "$server_image_id" "$FAKE_DOCKER_LOG" || fail 'Server rerun did not preserve the persisted immutable Server image ID'
[ "$(env_value "$tmp/server-home/.env" PORTLOOM_SSHD_IMAGE_ID)" = "$sshd_image_id" ] || fail 'Server rerun did not preserve the persisted immutable SSHD image ID'
! grep -q -- "sha256:$(printf 'd%.0s' {1..64})" "$FAKE_DOCKER_LOG" || fail 'Server rerun followed a locally moved mutable Server tag'
! grep -q -- "sha256:$(printf 'e%.0s' {1..64})" "$FAKE_DOCKER_LOG" || fail 'Server rerun followed a locally moved mutable SSHD tag'
admin_after=$(env_value "$tmp/server-home/.env" TM_ADMIN_TOKEN)
[ "$admin_before" = "$admin_after" ] || fail 'server rerun replaced administrator token'

cp -a "$tmp/server-home" "$tmp/server-no-image-id"
python3 - "$tmp/server-no-image-id/.env" <<'PYENV'
from pathlib import Path
import sys
p = Path(sys.argv[1])
p.write_text('\n'.join(line for line in p.read_text().splitlines() if not line.startswith(('PORTLOOM_SERVER_IMAGE_ID=', 'PORTLOOM_SSHD_IMAGE_ID='))) + '\n')
PYENV
expect_reject 'immutable server image identity is unavailable' env PORTLOOM_HOME="$tmp/server-no-image-id" "$server" --domain loom.example.com

cp -a "$tmp/server-no-image-id" "$tmp/server-running-image-id"
running_server_id="sha256:$(printf 'a%.0s' {1..64})"
running_sshd_id="sha256:$(printf 'c%.0s' {1..64})"
PORTLOOM_HOME="$tmp/server-running-image-id" \
  FAKE_DOCKER_INSPECT_SERVER_RESULT="$tmp/server-running-image-id|server|$running_server_id|true" \
  FAKE_DOCKER_INSPECT_SSHD_RESULT="$tmp/server-running-image-id|sshd|$running_sshd_id|true" \
  "$server" --domain loom.example.com >/dev/null
[ "$(env_value "$tmp/server-running-image-id/.env" PORTLOOM_SERVER_IMAGE_ID)" = "$running_server_id" ] || fail 'Server rerun did not migrate the running immutable Server image ID'
[ "$(env_value "$tmp/server-running-image-id/.env" PORTLOOM_SSHD_IMAGE_ID)" = "$running_sshd_id" ] || fail 'Server rerun did not migrate the running immutable SSHD image ID'

PORTLOOM_HOME="$tmp/server-home" "$server" --domain loom.example.com --version 0.3.1 >/dev/null
[ "$(env_value "$tmp/server-home/.env" PORTLOOM_SERVER_IMAGE)" = 'ghcr.io/lkhmm520/portloom-server:0.3.1' ] || fail 'native-edge rerun did not upgrade the pinned Server image'
[ "$(env_value "$tmp/server-home/.env" TM_ADMIN_TOKEN)" = "$admin_before" ] || fail 'native-edge version upgrade replaced administrator token'
for file in .env compose.yml; do
  [ -f "$tmp/server-home/native-upgrade-backup-0.3.1/$file" ] || fail "native upgrade did not back up $file"
done
native_upgrade_fail="$tmp/native-upgrade-failure"
cp -a "$tmp/server-home" "$native_upgrade_fail"
native_upgrade_output=''; native_upgrade_status=0
: > "$FAKE_DOCKER_LOG"
native_upgrade_output=$(env PORTLOOM_HOME="$native_upgrade_fail" PORTLOOM_EDGE_VERIFY_ATTEMPTS=1 FAKE_FAIL_DOCKER_MATCH='exec portloom-server curl' "$server" --domain loom.example.com --version 0.3.2 2>&1) || native_upgrade_status=$?
[ "$native_upgrade_status" -ne 0 ] || fail 'unhealthy native version upgrade unexpectedly succeeded'
printf '%s' "$native_upgrade_output" | grep -Fq 'native HTTPS edge did not become ready; restoring the previous native-edge deployment' || fail "missing native upgrade rollback message: $native_upgrade_output"
[ "$(env_value "$native_upgrade_fail/.env" PORTLOOM_SERVER_IMAGE)" = 'ghcr.io/lkhmm520/portloom-server:0.3.1' ] || fail 'failed native upgrade did not restore previous image configuration'
[ ! -e "$native_upgrade_fail/native-upgrade-backup-0.3.2" ] || fail 'failed native upgrade left a retry-blocking backup directory'
grep -q -- "compose --project-directory $native_upgrade_fail --env-file .env -f compose.yml up -d --pull never" "$FAKE_DOCKER_LOG" || fail 'native upgrade rollback allowed an implicit image pull'
grep -q '^existing-agent-key$' "$tmp/server-home/ssh-auth/authorized_keys" || fail 'server rerun replaced authorized_keys'
[ "$(stat -c %a "$tmp/server-home")" = 711 ] || fail 'server install root must be traverse-only for bind-mounted container UIDs'
for dir in "$tmp/server-home/server-data" "$tmp/server-home/server-data/certs" "$tmp/server-home/ssh-auth"; do
  [ "$(stat -c %a "$dir")" = 700 ] || fail "sensitive directory is not mode 0700: $dir"
done
[ "$(stat -c %a "$tmp/server-home/ssh-hostkeys")" = 755 ] || fail 'host-key directory must expose the public key to the unprivileged Server'
grep -q '^PORTLOOM_HTTP_PORT=80$' "$tmp/server-home/.env"
grep -q '^PORTLOOM_HTTPS_PORT=443$' "$tmp/server-home/.env"
! test -e "$tmp/server-home/Caddyfile" || fail 'native edge installer unexpectedly created a Caddyfile'

legacy="$tmp/legacy-server"
mkdir -p "$legacy/server-data" "$legacy/ssh-auth" "$legacy/ssh-hostkeys" "$legacy/caddy-data" "$legacy/caddy-config"
cat > "$legacy/.env" <<EOF
PORTLOOM_SERVER_IMAGE=ghcr.io/lkhmm520/portloom-server:0.2.0
PORTLOOM_SSHD_IMAGE=ghcr.io/lkhmm520/portloom-sshd:0.2.0
PORTLOOM_DOMAIN=loom.example.com
PORTLOOM_WEB_PORT=8080
PORTLOOM_SSH_PORT=2222
PORTLOOM_GATEWAY_PORT=8081
PORTLOOM_TLS_ASK_PORT=8082
PORTLOOM_CADDY_HTTP_PORT=80
PORTLOOM_CADDY_HTTPS_PORT=443
TM_ADMIN_TOKEN=$admin_before
PORTLOOM_TLS_ASK_TOKEN=$(printf 'b%.0s' {1..64})
EOF
printf 'services:\n  server: {}\n  caddy: {}\n' > "$legacy/compose.yml"
printf 'legacy caddy configuration\n' > "$legacy/Caddyfile"
legacy_fail="$tmp/legacy-server-failed-migration"
legacy_pull_fail="$tmp/legacy-server-failed-pull"
legacy_health_fail="$tmp/legacy-server-failed-health"
legacy_init_fail="$tmp/legacy-server-failed-init"
legacy_stop_fail="$tmp/legacy-server-failed-stop"
legacy_mutable="$tmp/legacy-server-mutable-latest"
cp -a "$legacy" "$legacy_mutable"
python3 - "$legacy_mutable/.env" <<'PYENV'
from pathlib import Path
import sys
p = Path(sys.argv[1])
s = p.read_text().replace('portloom-server:0.2.0', 'portloom-server:latest').replace('portloom-sshd:0.2.0', 'portloom-sshd:latest')
p.write_text(s)
PYENV
cp -a "$legacy" "$legacy_fail"
cp -a "$legacy" "$legacy_pull_fail"
cp -a "$legacy" "$legacy_health_fail"
cp -a "$legacy" "$legacy_init_fail"
cp -a "$legacy" "$legacy_stop_fail"
export FAKE_DOCKER_INSPECT_AUTO_RUNNING=1
expect_reject 'legacy Caddy installation detected; rerun with --migrate-native-edge' env PORTLOOM_HOME="$legacy" "$server" --domain loom.example.com --version 0.3.0
expect_reject 'legacy migration target must use a Server image reference different from the existing deployment' env PORTLOOM_HOME="$legacy_mutable" "$server" --domain loom.example.com --migrate-native-edge
expect_reject 'injected docker failure' env PORTLOOM_HOME="$legacy_pull_fail" FAKE_FAIL_DOCKER_MATCH='pull ghcr.io/lkhmm520/portloom-server:0.3.0' "$server" --domain loom.example.com --version 0.3.0 --migrate-native-edge
[ ! -e "$legacy_pull_fail/migration-backup-v0.3.0" ] || fail 'failed preflight left a migration backup directory'
grep -q '^PORTLOOM_CADDY_HTTP_PORT=80$' "$legacy_pull_fail/.env" || fail 'failed preflight modified legacy .env'
expect_reject 'injected docker failure' env PORTLOOM_HOME="$legacy_init_fail" FAKE_FAIL_DOCKER_MATCH='run --pull=never --rm --user 0:0' "$server" --domain loom.example.com --version 0.3.0 --migrate-native-edge
[ ! -e "$legacy_init_fail/migration-backup-v0.3.0" ] || fail 'failed init left a migration backup directory'
grep -q '^PORTLOOM_CADDY_HTTP_PORT=80$' "$legacy_init_fail/.env" || fail 'failed init modified legacy .env'
: > "$FAKE_DOCKER_LOG"
expect_reject 'native-edge startup failed; restoring the legacy Caddy deployment' env PORTLOOM_HOME="$legacy_stop_fail" FAKE_FAIL_DOCKER_MATCH='stop caddy' "$server" --domain loom.example.com --version 0.3.0 --migrate-native-edge
[ ! -e "$legacy_stop_fail/migration-backup-v0.3.0" ] || fail 'failed Caddy stop left a retry-blocking backup directory'
grep -q '^PORTLOOM_CADDY_HTTP_PORT=80$' "$legacy_stop_fail/.env" || fail 'failed Caddy stop did not restore legacy .env'
: > "$FAKE_DOCKER_LOG"
expect_reject 'native-edge startup failed; restoring the legacy Caddy deployment' env PORTLOOM_HOME="$legacy_fail" FAKE_FAIL_DOCKER_MATCH='up -d --remove-orphans' "$server" --domain loom.example.com --version 0.3.0 --migrate-native-edge
[ ! -e "$legacy_fail/migration-backup-v0.3.0" ] || fail 'failed migration left a retry-blocking backup directory'
grep -q '^PORTLOOM_CADDY_HTTP_PORT=80$' "$legacy_fail/.env" || fail 'failed migration did not restore legacy .env'
grep -q '^  caddy: {}$' "$legacy_fail/compose.yml" || fail 'failed migration did not restore legacy Compose'
grep -q '^legacy caddy configuration$' "$legacy_fail/Caddyfile" || fail 'failed migration did not restore legacy Caddyfile'
grep -q -- "compose --project-directory $legacy_fail --env-file .env -f compose.yml up -d --pull never" "$FAKE_DOCKER_LOG" || fail 'failed migration did not restart canonical legacy Compose without implicit pulls from the install project directory'
: > "$FAKE_DOCKER_LOG"
health_output=''; health_status=0
health_output=$(env PORTLOOM_HOME="$legacy_health_fail" PORTLOOM_EDGE_VERIFY_ATTEMPTS=1 FAKE_FAIL_DOCKER_MATCH='exec portloom-server curl' "$server" --domain loom.example.com --version 0.3.0 --migrate-native-edge 2>&1) || health_status=$?
[ "$health_status" -ne 0 ] || fail 'unhealthy native edge migration unexpectedly succeeded'
printf '%s' "$health_output" | grep -Fq 'native HTTPS edge did not become ready; restoring the legacy Caddy deployment' || fail "missing primary readiness failure: $health_output"
printf '%s' "$health_output" | grep -Fq 'automatic legacy restoration could not be verified' || fail "missing recovery verification failure: $health_output"
[ ! -e "$legacy_health_fail/migration-backup-v0.3.0" ] || fail 'failed health verification left a retry-blocking backup directory'
grep -q '^PORTLOOM_CADDY_HTTP_PORT=80$' "$legacy_health_fail/.env" || fail 'failed health verification did not restore legacy .env'
: > "$FAKE_DOCKER_LOG"
PORTLOOM_HOME="$legacy" "$server" --domain loom.example.com --version 0.3.0 --migrate-native-edge >/dev/null
for file in .env compose.yml Caddyfile; do
  [ -f "$legacy/migration-backup-v0.3.0/$file" ] || fail "legacy migration did not back up $file"
done
grep -q '^PORTLOOM_HTTP_PORT=80$' "$legacy/.env" || fail 'legacy migration did not map Caddy HTTP port'
grep -q '^PORTLOOM_HTTPS_PORT=443$' "$legacy/.env" || fail 'legacy migration did not map Caddy HTTPS port'
grep -q -- "compose --project-directory $legacy --env-file migration-backup-v0.3.0/.env -f migration-backup-v0.3.0/compose.yml stop caddy" "$FAKE_DOCKER_LOG" || fail 'legacy migration did not stop Caddy from the install project directory'
grep -q -- 'compose .*compose.yml up -d --remove-orphans' "$FAKE_DOCKER_LOG" || fail 'legacy migration did not remove the old Caddy orphan'
grep -q 'PORTLOOM_SERVER_IMAGE="$previous_server_image_id" PORTLOOM_SSHD_IMAGE="$previous_sshd_image_id" docker compose' "$server" || fail 'legacy rollback does not pin the previous immutable Server/SSHD image IDs'
unset FAKE_DOCKER_INSPECT_AUTO_RUNNING

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
PORTLOOM_AGENT_IMAGE_ID=sha256:$(printf 'b%.0s' {1..64})
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
! grep -q '^pull ' "$FAKE_DOCKER_LOG" || fail 'Agent recovery pulled an existing mutable image reference'
! grep -q '^run --rm ' "$FAKE_DOCKER_LOG" || fail 'Agent helper container allowed an implicit image pull'
grep -q '^run --pull=never --rm ' "$FAKE_DOCKER_LOG" || fail 'Agent helper containers did not disable implicit pulls'
[ "$(grep -c -- 'compose .* up -d --force-recreate --pull never' "$FAKE_DOCKER_LOG")" -ge 2 ] || fail 'Agent recovery Compose activation allowed an implicit image pull'
[ "$(grep -c -- '--entrypoint /bin/rm .*managed-ssh.ready' "$FAKE_DOCKER_LOG")" -ge 2 ] || fail 'recovery did not clear readiness before both restarts'
[ "$(grep -c -- '--force-recreate' "$FAKE_DOCKER_LOG")" -ge 2 ] || fail 'recovery did not verify both credential-bearing and token-free restarts'
grep -q '^\[vps.example.com\]:2222 ssh-ed25519 AAAA$' "$tmp/recover-agent/data/ssh/known_hosts" || fail 'recovery did not apply the supplied SSH host key'
grep -q -- "sha256:$(printf 'b%.0s' {1..64})" "$FAKE_DOCKER_LOG" || fail 'Agent recovery did not use its persisted immutable image ID'
ready_nonce=$(env_value "$tmp/recover-agent/.env" TM_MANAGED_SSH_READY_NONCE)
[ "$(tr -d '\n' < "$tmp/recover-agent/data/managed-ssh.ready")" = "$ready_nonce" ] || fail 'recovery accepted a marker from another container generation'
[ "$(sort -u "$FAKE_NONCE_LOG" | wc -l)" -eq "$(wc -l < "$FAKE_NONCE_LOG")" ] || fail 'a readiness nonce was reused across container generations'
immutable_agent_image="sha256:$(printf 'b%.0s' {1..64})"
: > "$FAKE_DOCKER_LOG"
PORTLOOM_HOME="$tmp/recover-agent" FAKE_AGENT_READY=1 FAKE_DOCKER_INSPECT_RESULT="$tmp/recover-agent|agent|$immutable_agent_image|true" "$agent" "${base_agent[@]}" >/dev/null
grep -q -- "$immutable_agent_image" "$FAKE_DOCKER_LOG" || fail 'Agent recovery did not preserve the running container immutable image ID'
! grep -q '^pull ' "$FAKE_DOCKER_LOG" || fail 'immutable Agent recovery pulled a mutable image reference'
expect_reject 'running Agent identity does not match this install directory' env PORTLOOM_HOME="$tmp/recover-agent" FAKE_DOCKER_INSPECT_RESULT="$tmp/other-agent|agent|$immutable_agent_image|true" "$agent" "${base_agent[@]}"
expect_reject 'running Agent container is not active' env PORTLOOM_HOME="$tmp/recover-agent" FAKE_DOCKER_INSPECT_RESULT="$tmp/recover-agent|agent|$immutable_agent_image|false" "$agent" "${base_agent[@]}"
old_nonce=$(head -n 1 "$FAKE_NONCE_LOG")
expect_reject 'was not ready after 1 attempts' env PORTLOOM_HOME="$tmp/recover-agent" FAKE_AGENT_READY=stale PORTLOOM_READY_ATTEMPTS=1 "$agent" "${base_agent[@]}"
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
rebuild_output=$(PORTLOOM_HOME="$tmp/rebuild-agent" FAKE_AGENT_READY=1 "$agent" "${base_agent[@]}")
printf '%s' "$rebuild_output" | grep -Fq '[1/8] Checking host prerequisites' || fail "Agent install did not show prerequisite progress: $rebuild_output"
printf '%s' "$rebuild_output" | grep -Fq '[5/8] Starting Agent and enrolling' || fail "Agent install did not show enrollment progress: $rebuild_output"
printf '%s' "$rebuild_output" | grep -Fq '[8/8] Verifying token-free restart' || fail "Agent install did not show final verification progress: $rebuild_output"
printf '%s' "$rebuild_output" | grep -Fq 'PortLoom Agent is installed and enrolled as nas.' || fail "Agent install did not show completion: $rebuild_output"
[[ "$(env_value "$tmp/rebuild-agent/.env" PORTLOOM_AGENT_IMAGE_ID)" =~ ^sha256:[0-9a-f]{64}$ ]] || fail 'fresh Agent install did not persist an immutable image ID'
ssh-keygen -lf "$tmp/rebuild-agent/data/ssh/id_ed25519.pub" >/dev/null || fail 'missing public key was not rebuilt from private key'
grep -q 'ssh-keygen.*-y' "$FAKE_DOCKER_LOG" || fail 'ssh-keygen -y was not used to rebuild public key'
[ -f "$tmp/rebuild-agent/data/managed-ssh.ready" ] || fail 'managed SSH readiness marker was not observed'
! grep -q '^TM_ENROLLMENT_TOKEN=' "$tmp/rebuild-agent/.env" || fail 'one-time token remained after managed SSH readiness'

cp -a "$tmp/recover-agent" "$tmp/recover-agent-no-id"
python3 - "$tmp/recover-agent-no-id/.env" <<'PYENV'
from pathlib import Path
import sys
p = Path(sys.argv[1])
p.write_text('\n'.join(line for line in p.read_text().splitlines() if not line.startswith('PORTLOOM_AGENT_IMAGE_ID=')) + '\n')
PYENV
expect_reject 'immutable Agent image identity is unavailable' env PORTLOOM_HOME="$tmp/recover-agent-no-id" FAKE_AGENT_READY=1 "$agent" "${base_agent[@]}"

echo 'installer_contracts=ok'
