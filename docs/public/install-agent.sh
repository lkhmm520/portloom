#!/usr/bin/env bash
set -euo pipefail
usage() { cat <<'EOF'
Install PortLoom Agent on the internal Docker host (NAS/home server).

Usage: install-agent.sh --server-url URL --name NAME --token TOKEN \
  --ssh-host HOST --ssh-port PORT --ssh-host-key 'ssh-ed25519 AAAA...'
Options:
  --home PATH       Install directory (default: ~/.portloom/agent)
  --version TAG     PortLoom image tag (default: latest)
EOF
}
progress() { printf '[%s/%s] %s\n' "$1" "$2" "$3"; }
resolve_flock() {
  local candidate
  candidate=$(command -v flock 2>/dev/null || true)
  if [ -n "$candidate" ] && [ -x "$candidate" ]; then printf '%s' "$candidate"; return 0; fi
  for candidate in /opt/bin/flock /opt/sbin/flock; do
    if [ -x "$candidate" ]; then printf '%s' "$candidate"; return 0; fi
  done
  return 1
}
server_url=""; name=""; token=""; ssh_host=""; ssh_port=""; ssh_host_key=""; home="${PORTLOOM_HOME:-$HOME/.portloom/agent}"; version=latest
while [ "$#" -gt 0 ]; do
  case "$1" in
    --server-url) server_url=${2:-}; shift 2;; --name) name=${2:-}; shift 2;; --token) token=${2:-}; shift 2;;
    --ssh-host) ssh_host=${2:-}; shift 2;; --ssh-port) ssh_port=${2:-}; shift 2;; --ssh-host-key) ssh_host_key=${2:-}; shift 2;;
    --home) home=${2:-}; shift 2;; --version) version=${2:-}; shift 2;; -h|--help) usage; exit 0;; *) echo "Unknown option: $1" >&2; usage >&2; exit 2;;
  esac
done
has_control() { local LC_ALL=C; [[ "$1" =~ [[:cntrl:]] ]]; }
valid_url_port() { [ -z "${1:-}" ] || { [ "$1" -ge 1 ] 2>/dev/null && [ "$1" -le 65535 ]; }; }
validate_server_origin() {
  local parsed_port=""
  has_control "$server_url" && return 1
  if [[ "$server_url" =~ ^https://(\[[0-9A-Fa-f]*:[0-9A-Fa-f:.]*\]|[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?)(:([0-9]{1,5}))?$ ]]; then
    parsed_port=${BASH_REMATCH[4]:-}
    valid_url_port "$parsed_port"
    return
  fi
  if [[ "$server_url" =~ ^http://(localhost|127(\.[0-9]{1,3}){3}|\[::1\])(:([0-9]{1,5}))?$ ]]; then
    parsed_port=${BASH_REMATCH[4]:-}
    valid_url_port "$parsed_port" || return 1
    allow_http=true
    return 0
  fi
  return 1
}
validate_server_origin || { echo 'invalid --server-url: use an HTTPS origin without userinfo, path, query, or fragment (loopback HTTP is test-only)' >&2; exit 2; }
[[ "$name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || { echo 'unsafe or empty Agent name' >&2; exit 2; }
[[ "$token" =~ ^[0-9A-Fa-f]{64}$ ]] || { echo 'invalid enrollment token' >&2; exit 2; }
! has_control "$ssh_host" && [[ "$ssh_host" =~ ^[A-Za-z0-9][A-Za-z0-9.:-]{0,252}$ ]] || { echo 'invalid SSH host' >&2; exit 2; }
[[ "$ssh_port" =~ ^[0-9]+$ ]] && [ "$ssh_port" -ge 1 ] && [ "$ssh_port" -le 65535 ] || { echo 'invalid SSH port' >&2; exit 2; }
[[ "$ssh_host_key" =~ ^ssh-ed25519\ [A-Za-z0-9+/=]+$ ]] || { echo 'invalid SSH host key' >&2; exit 2; }
if [ -z "$home" ] || has_control "$home"; then
  echo 'invalid --home: control characters are not allowed' >&2
  exit 2
fi
[[ "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || { echo 'invalid --version' >&2; exit 2; }
progress 1 8 'Checking host prerequisites'
command -v docker >/dev/null || { echo 'Docker is required' >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo 'Docker Compose v2 is required' >&2; exit 1; }
command -v sha256sum >/dev/null || { echo 'sha256sum is required' >&2; exit 1; }
flock_bin=$(resolve_flock) || {
  echo 'flock is required to serialize Agent installation' >&2
  echo 'QNAP/Entware: run /opt/bin/opkg install flock, then rerun this command.' >&2
  exit 1
}
ready_attempts=${PORTLOOM_READY_ATTEMPTS:-60}
[[ "$ready_attempts" =~ ^[0-9]+$ ]] && [ "$ready_attempts" -ge 1 ] && [ "$ready_attempts" -le 600 ] || { echo 'PORTLOOM_READY_ATTEMPTS must be within 1..600' >&2; exit 2; }
umask 077
image="${PORTLOOM_AGENT_IMAGE_OVERRIDE:-ghcr.io/lkhmm520/portloom-agent:$version}"
progress 2 8 'Preparing the install directory'
portloom_root="$HOME/.portloom"
case "$home" in
  "$portloom_root"|"$portloom_root"/*) mkdir -p "$portloom_root"; chmod 0711 "$portloom_root";;
esac
mkdir -p "$home"
home=$(cd "$home" && pwd -L)
chmod 0711 "$home"
exec 9>"$home/.install.lock"
chmod 0600 "$home/.install.lock"
"$flock_bin" -n 9 || { echo "another Agent installation is already running in $home" >&2; exit 1; }
# Prevent long-running Docker children from keeping the installer lock after this shell exits.
docker() { command docker "$@" 9>&-; }
docker_run() { docker run --pull=never "$@"; }
env_value() {
  local wanted=$1 key value
  [ -f "$home/.env" ] || return 1
  while IFS='=' read -r key value; do
    if [ "$key" = "$wanted" ]; then printf '%s' "$value"; return 0; fi
  done < "$home/.env"
  return 1
}
valid_image_id() { [[ "${1:-}" =~ ^sha256:[0-9a-f]{64}$ ]]; }
image_id_for_ref() {
  local resolved
  resolved=$(docker image inspect --format '{{.Id}}' "$1" 2>/dev/null) || return 1
  valid_image_id "$resolved" || return 1
  printf '%s' "$resolved"
}
set_env_value() {
  local wanted=$1 value=$2 key current tmp="$home/.env.next"
  while IFS='=' read -r key current; do
    [ "$key" = "$wanted" ] || printf '%s=%s\n' "$key" "$current"
  done < "$home/.env" > "$tmp"
  printf '%s=%s\n' "$wanted" "$value" >> "$tmp"
  chmod 600 "$tmp"
  mv "$tmp" "$home/.env"
}
wait_ready() {
  local ready_image=$1 expected_nonce=$2 observed attempt=1
  while [ "$attempt" -le "$ready_attempts" ]; do
    observed=$(docker_run --rm --user 65532:65532 -v "$home/data:/data:ro" --entrypoint /bin/cat "$ready_image" /data/managed-ssh.ready 2>/dev/null || true) 9>&-
    [ "$observed" = "$expected_nonce" ] && return 0
    if [ $((attempt % 10)) -eq 0 ]; then
      printf 'Still waiting for Agent readiness (%s/%s)...\n' "$attempt" "$ready_attempts"
    fi
    sleep 1
    attempt=$((attempt + 1))
  done
  return 1
}
rotate_ready_nonce() {
  local tmp_env="$home/.env.next" uuid generation
  [ -r /proc/sys/kernel/random/uuid ] || { echo 'kernel UUID source is required for readiness generation IDs' >&2; exit 1; }
  uuid=$(tr -d '-' < /proc/sys/kernel/random/uuid)
  [[ "$uuid" =~ ^[0-9a-f]{32}$ ]] || { echo 'failed to generate readiness ID' >&2; exit 1; }
  generation=$(env_value TM_MANAGED_SSH_READY_GENERATION || true)
  [[ "$generation" =~ ^[0-9]+$ ]] || generation=0
  [ "$generation" -lt 9223372036854775807 ] || { echo 'readiness generation exhausted' >&2; exit 1; }
  generation=$((generation + 1))
  ready_nonce="${generation}-${uuid}"
  while IFS= read -r line; do case "$line" in TM_MANAGED_SSH_READY_NONCE=*|TM_MANAGED_SSH_READY_GENERATION=*) ;; *) printf '%s\n' "$line";; esac; done < "$home/.env" > "$tmp_env"
  printf 'TM_MANAGED_SSH_READY_GENERATION=%s\nTM_MANAGED_SSH_READY_NONCE=%s\n' "$generation" "$ready_nonce" >> "$tmp_env"
  chmod 600 "$tmp_env"
  mv "$tmp_env" "$home/.env"
}
remove_ready() {
  local ready_image=$1
  docker_run --rm --user 65532:65532 -v "$home/data:/data" --entrypoint /bin/rm "$ready_image" -f /data/managed-ssh.ready
}
write_known_hosts() {
  local key_image=$1
  docker_run --rm --user 65532:65532 -e HOST_LABEL="$host_label" -e HOST_KEY="$ssh_host_key" \
    -v "$home/data:/data" --entrypoint /bin/sh "$key_image" -c 'umask 077; mkdir -p /data/ssh; printf "%s %s\n" "$HOST_LABEL" "$HOST_KEY" > /data/ssh/known_hosts.next; mv /data/ssh/known_hosts.next /data/ssh/known_hosts'
}
scrub_enrollment_token() {
  local tmp_env="$home/.env.next"
  while IFS= read -r line; do case "$line" in TM_ENROLLMENT_TOKEN=*) ;; *) printf '%s\n' "$line";; esac; done < "$home/.env" > "$tmp_env"
  chmod 600 "$tmp_env"
  mv "$tmp_env" "$home/.env"
}
write_agent_compose() {
  cat > "$home/compose.yml.next" <<'EOF'
name: portloom-agent
services:
  agent:
    image: ${PORTLOOM_AGENT_IMAGE_ID}
    container_name: portloom-agent
    restart: unless-stopped
    network_mode: host
    env_file: [.env]
    environment:
      TM_AGENT_STATE_PATH: /data/agent.json
    volumes: [./data:/data]
    read_only: true
    tmpfs:
      - /tmp:size=16m,mode=1777
      - /home/tunnel/.ssh:size=1m,uid=65532,gid=65532,mode=0700
    security_opt: [no-new-privileges:true]
    cap_drop: [ALL]
EOF
  chmod 600 "$home/compose.yml.next"
  mv "$home/compose.yml.next" "$home/compose.yml"
}
host_label="$ssh_host"; [ "$ssh_port" = 22 ] || host_label="[$ssh_host]:$ssh_port"
host_key_sha256=$(printf '%s' "$ssh_host_key" | sha256sum | cut -d ' ' -f 1)
if [ -f "$home/.env" ] || [ -f "$home/compose.yml" ]; then
  progress 3 8 'Validating the existing Agent identity'
  [ -f "$home/.env" ] || { echo 'existing Agent compose.yml has no .env; manual recovery is required' >&2; exit 1; }
  [ "$(env_value TM_SERVER_URL || true)" = "$server_url" ] &&
    [ "$(env_value TM_CLIENT_NAME || true)" = "$name" ] &&
    [ "$(env_value TM_SSH_HOST || true)" = "$ssh_host" ] &&
    [ "$(env_value TM_SSH_PORT || true)" = "$ssh_port" ] &&
    [ "$(env_value PORTLOOM_SSH_HOST_KEY_SHA256 || true)" = "$host_key_sha256" ] || {
      echo 'existing Agent state does not match this install command; choose the original --home or a new --home' >&2
      exit 1
    }
  existing_image=$(env_value PORTLOOM_AGENT_IMAGE || true)
  existing_image_id=$(env_value PORTLOOM_AGENT_IMAGE_ID || true)
  [ -n "$existing_image" ] || { echo 'existing Agent image is missing from .env' >&2; exit 1; }
  [ "$existing_image" = "$image" ] || { echo 'existing Agent image does not match this install command; use the original --version' >&2; exit 1; }
  if [ -n "$existing_image_id" ] && ! valid_image_id "$existing_image_id"; then
    echo 'existing Agent immutable image identity is invalid' >&2
    exit 1
  fi
  running_identity=$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}|{{ index .Config.Labels "com.docker.compose.service" }}|{{ .Image }}|{{ .State.Running }}' portloom-agent 2>/dev/null || true)
  recovery_image=""
  if [ -n "$running_identity" ]; then
    IFS='|' read -r running_home running_service recovery_image running_state trailing_identity <<< "$running_identity"
    if [ -n "$trailing_identity" ] || [ "$running_home" != "$home" ] || [ "$running_service" != agent ] || ! valid_image_id "$recovery_image"; then
      echo 'running Agent identity does not match this install directory' >&2
      exit 1
    fi
    [ "$running_state" = true ] || { echo 'running Agent container is not active; start the original container or restore its immutable image ID before rerunning' >&2; exit 1; }
    if [ -n "$existing_image_id" ] && [ "$existing_image_id" != "$recovery_image" ]; then
      echo 'running Agent image does not match the persisted immutable image identity' >&2
      exit 1
    fi
  fi
  [ -n "$recovery_image" ] || recovery_image=$existing_image_id
  valid_image_id "$recovery_image" || { echo 'immutable Agent image identity is unavailable; restore the original container or image ID before recovery' >&2; exit 1; }
  set_env_value PORTLOOM_AGENT_IMAGE_ID "$recovery_image"
  write_agent_compose
  progress 4 8 'Refreshing the Agent SSH trust'
  if docker_run --rm --user 65532:65532 -v "$home/data:/data:ro" --entrypoint /usr/bin/test "$recovery_image" -L /data/agent.json; then
    echo 'refusing to recover from a symlinked agent.json' >&2
    exit 1
  fi
  write_known_hosts "$recovery_image"
  rotate_ready_nonce
  remove_ready "$recovery_image"
  progress 5 8 'Restarting the existing Agent'
  (cd "$home" && PORTLOOM_AGENT_IMAGE_ID="$recovery_image" docker compose --env-file .env -f compose.yml up -d --force-recreate --pull never) 9>&-
  progress 6 8 'Waiting for managed SSH and initial synchronization'
  if ! wait_ready "$recovery_image" "$ready_nonce"; then
    echo "Existing Agent was not ready after $ready_attempts attempts; fix connectivity and rerun the same command" >&2
    exit 1
  fi
  progress 7 8 'Removing the one-time enrollment credential'
  scrub_enrollment_token
  rotate_ready_nonce
  remove_ready "$recovery_image"
  (cd "$home" && PORTLOOM_AGENT_IMAGE_ID="$recovery_image" docker compose --env-file .env -f compose.yml up -d --force-recreate --pull never) 9>&-
  progress 8 8 'Verifying token-free restart and heartbeat'
  if ! wait_ready "$recovery_image" "$ready_nonce"; then
    echo 'Agent credentials were recovered, but SSH did not recover after removing the enrollment token; rerun after fixing connectivity' >&2
    exit 1
  fi
  printf '\nPortLoom Agent recovery completed for %s.\nFiles: %s\n' "$name" "$home"
  exit 0
fi
progress 3 8 'Pulling and pinning the Agent image'
if [ "${PORTLOOM_SKIP_PULL:-false}" != true ]; then docker pull "$image" >/dev/null; fi
image_ref=$image
image=$(image_id_for_ref "$image_ref") || { echo "unable to resolve immutable Agent image ID for $image_ref" >&2; exit 1; }
if [ -d "$home/data" ]; then
  if docker_run --rm --user 65532:65532 -v "$home/data:/data:ro" --entrypoint /usr/bin/test "$image" -e /data/agent.json ||
     docker_run --rm --user 65532:65532 -v "$home/data:/data:ro" --entrypoint /usr/bin/test "$image" -e /data/agent.json.pending; then
    echo 'Agent credentials exist without .env and compose.yml; manual recovery is required to avoid overwriting identity' >&2
    exit 1
  fi
fi
progress 4 8 'Preparing the Agent identity and SSH trust'
mkdir -p "$home/data/ssh"
docker_run --rm --user 0:0 -v "$home/data:/data" --entrypoint /bin/chown "$image" -R 65532:65532 /data
docker_run --rm --user 65532:65532 -v "$home/data:/data" --entrypoint /bin/rm "$image" -f /data/managed-ssh.ready
if ! docker_run --rm --user 65532:65532 -v "$home/data:/data:ro" --entrypoint /usr/bin/test "$image" -s /data/ssh/id_ed25519; then
  docker_run --rm --user 65532:65532 -v "$home/data:/data" --entrypoint /bin/rm "$image" -f /data/ssh/id_ed25519.pub
  docker_run --rm --user 65532:65532 -v "$home/data:/data" --entrypoint /usr/bin/ssh-keygen "$image" \
    -q -t ed25519 -a 64 -N '' -C "portloom-agent:$name" -f /data/ssh/id_ed25519
elif ! docker_run --rm --user 65532:65532 -v "$home/data:/data:ro" --entrypoint /usr/bin/test "$image" -s /data/ssh/id_ed25519.pub; then
  docker_run --rm --user 65532:65532 -v "$home/data:/data" --entrypoint /bin/sh "$image" -c \
    'umask 077; ssh-keygen -y -f /data/ssh/id_ed25519 > /data/ssh/id_ed25519.pub.next; mv /data/ssh/id_ed25519.pub.next /data/ssh/id_ed25519.pub'
fi
{
  printf 'PORTLOOM_AGENT_IMAGE=%s\nPORTLOOM_AGENT_IMAGE_ID=%s\nPORTLOOM_SSH_HOST_KEY_SHA256=%s\n' "$image_ref" "$image" "$host_key_sha256"
  printf 'TM_SERVER_URL=%s\nTM_CLIENT_NAME=%s\nTM_ENROLLMENT_TOKEN=%s\n' "$server_url" "$name" "$token"
  printf 'TM_SSH_HOST=%s\nTM_SSH_PORT=%s\nTM_SSH_USER=tunnel\n' "$ssh_host" "$ssh_port"
  printf 'TM_SSH_IDENTITY_FILE=/data/ssh/id_ed25519\nTM_SSH_PUBLIC_KEY_FILE=/data/ssh/id_ed25519.pub\nTM_SSH_KNOWN_HOSTS_FILE=/data/ssh/known_hosts\n'
  printf 'TM_MANAGED_SSH_ISOLATED=true\nTM_MANAGED_SSH_READY_PATH=/data/managed-ssh.ready\n'
  [ "${allow_http:-false}" = true ] && printf 'TM_ALLOW_INSECURE_HTTP=true\n'
} > "$home/.env.next"
chmod 600 "$home/.env.next"
mv "$home/.env.next" "$home/.env"
write_known_hosts "$image"
write_agent_compose
rotate_ready_nonce
remove_ready "$image"
progress 5 8 'Starting Agent and enrolling'
(cd "$home" && PORTLOOM_AGENT_IMAGE_ID="$image" docker compose --env-file .env -f compose.yml up -d --pull never) 9>&-
progress 6 8 'Waiting for managed SSH and initial synchronization'
if ! wait_ready "$image" "$ready_nonce"; then
  echo "Agent was not ready after $ready_attempts attempts; its consumed credentials were saved and rerunning the same command will resume safely" >&2
  exit 1
fi
progress 7 8 'Removing the one-time enrollment credential'
scrub_enrollment_token
rotate_ready_nonce
remove_ready "$image"
(cd "$home" && PORTLOOM_AGENT_IMAGE_ID="$image" docker compose --env-file .env -f compose.yml up -d --force-recreate --pull never) 9>&-
progress 8 8 'Verifying token-free restart'
if ! wait_ready "$image" "$ready_nonce"; then
  echo 'Agent enrolled, but SSH did not recover after removing the enrollment token; rerun after fixing connectivity' >&2
  exit 1
fi
printf '\nPortLoom Agent is installed and enrolled as %s.\nOpen the Server WebUI to add routes.\nFiles: %s\n' "$name" "$home"
