#!/usr/bin/env bash
set -euo pipefail
# NAS PATH quirks: Synology keeps docker in /usr/local/bin, QNAP Container
# Station and Entware use /share/CACHEDEV*/... symlinked via /usr/local and
# /opt. Appending (not prepending) keeps caller-provided overrides first.
PATH="$PATH:/usr/local/bin:/usr/local/sbin:/opt/bin:/opt/sbin"
usage() { cat <<'EOF'
Install PortLoom Agent on the internal Docker host (NAS/home server).

Usage: install-agent.sh --server-url URL --name NAME --token TOKEN \
  --ssh-host HOST --ssh-port PORT --ssh-host-key 'ssh-ed25519 AAAA...'
Options:
  --home PATH       Install directory (default: ~/.portloom/agent)
  --version TAG     PortLoom image tag (default: latest)
EOF
}
total_steps=8
current_step_label='checking arguments'
install_success=false
lock_dir=''
color() { if [ -t 1 ]; then printf '\033[%sm%s\033[0m' "$1" "$2"; else printf '%s' "$2"; fi }
progress() {
  current_step_label=$3
  local filled=$1 total=$2 bar='' i
  for ((i = 1; i <= total; i++)); do
    if [ "$i" -le "$filled" ]; then bar="${bar}#"; else bar="${bar}-"; fi
  done
  printf '%s [%s/%s] %s\n' "$(color '1;32' "[$bar]")" "$1" "$2" "$3"
}
on_exit() {
  local status=$?
  if [ -n "$lock_dir" ]; then rmdir "$lock_dir" 2>/dev/null || true; fi
  if [ "$status" -ne 0 ] && [ "$install_success" != true ]; then
    {
      printf '\n%s Agent installation FAILED at: %s (exit %s)\n' "$(color '1;31' '✗')" "$current_step_label" "$status"
      printf '安装失败（步骤：%s）。请根据上方错误信息修复后，重新执行同一条安装命令即可安全续装。\n' "$current_step_label"
      printf 'Fix the error above and rerun the exact same install command; reruns resume safely.\n'
    } >&2
  fi
}
trap on_exit EXIT
resolve_flock() {
  local candidate
  candidate=$(command -v flock 2>/dev/null || true)
  if [ -n "$candidate" ] && [ -x "$candidate" ]; then printf '%s' "$candidate"; return 0; fi
  for candidate in /opt/bin/flock /opt/sbin/flock; do
    if [ -x "$candidate" ]; then printf '%s' "$candidate"; return 0; fi
  done
  return 1
}
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then printf '%s' "$1" | sha256sum | cut -d ' ' -f 1
  elif command -v shasum >/dev/null 2>&1; then printf '%s' "$1" | shasum -a 256 | cut -d ' ' -f 1
  else printf '%s' "$1" | openssl dgst -sha256 -r | cut -d ' ' -f 1
  fi
}
random_hex32() {
  if [ -r /proc/sys/kernel/random/uuid ]; then tr -d '-' < /proc/sys/kernel/random/uuid
  else od -An -N16 -tx1 /dev/urandom | tr -d ' \n'
  fi
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
command -v docker >/dev/null || {
  echo 'Docker is required but was not found in PATH.' >&2
  echo '未找到 docker 命令。Synology 请安装 Container Manager；QNAP 请安装 Container Station；其他系统请先安装 Docker Engine。' >&2
  exit 1
}
if [ "${PORTLOOM_SKIP_DAEMON_CHECK:-false}" != true ] && ! docker info >/dev/null 2>&1; then
  echo 'Docker is installed but the daemon is not reachable for this user.' >&2
  echo 'Docker 已安装但当前用户无法访问守护进程。请确认 Docker 服务已启动，并将当前用户加入 docker 组（或改用 root/管理员执行）。' >&2
  echo "Try: sudo usermod -aG docker $(id -un 2>/dev/null || echo '<user>') && re-login, or rerun with sudo." >&2
  exit 1
fi
compose_style=''
if docker compose version >/dev/null 2>&1; then
  compose_style=plugin
elif command -v docker-compose >/dev/null 2>&1; then
  standalone_version=$(docker-compose version --short 2>/dev/null || true)
  case "$standalone_version" in
    2*|v2*) compose_style=standalone; echo "Using standalone docker-compose $standalone_version (compose plugin not found).";;
    *)
      echo "docker-compose $standalone_version is too old; Compose v2 is required." >&2
      echo '检测到旧版 docker-compose（v1）。请升级到 Compose v2：Synology 升级 Container Manager；QNAP 升级 Container Station 3；其他系统安装 docker-compose-plugin。' >&2
      exit 1;;
  esac
else
  echo 'Docker Compose v2 is required (neither "docker compose" nor docker-compose v2 was found).' >&2
  echo '未找到 Docker Compose v2。Synology 请升级 Container Manager；QNAP 请升级 Container Station 3；其他系统请安装 docker-compose-plugin。' >&2
  exit 1
fi
run_compose() {
  if [ "$compose_style" = plugin ]; then docker compose "$@"; else command docker-compose "$@" 9>&-; fi
}
command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1 || command -v openssl >/dev/null 2>&1 || {
  echo 'sha256sum, shasum, or openssl is required to fingerprint the SSH host key.' >&2
  echo '需要 sha256sum、shasum 或 openssl 之一（几乎所有 NAS 都自带其中一个）。' >&2
  exit 1
}
if ! flock_bin=$(resolve_flock); then
  flock_bin=''
  echo 'flock not found; falling back to a directory lock (QNAP/Entware can install it with: /opt/bin/opkg install flock).'
fi
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
if [ -n "$flock_bin" ]; then
  "$flock_bin" -n 9 || { echo "another Agent installation is already running in $home" >&2; exit 1; }
else
  # Portable fallback: mkdir is atomic on every NAS filesystem.
  if ! mkdir "$home/.install.lock.d" 2>/dev/null; then
    echo "another Agent installation is already running in $home" >&2
    echo "若确认没有其他安装在运行（例如上次安装被强制中断），删除目录 $home/.install.lock.d 后重试。" >&2
    exit 1
  fi
  lock_dir="$home/.install.lock.d"
fi
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
  uuid=$(random_hex32)
  [[ "$uuid" =~ ^[0-9a-f]{32}$ ]] || { echo 'failed to generate readiness ID (no kernel UUID source or usable /dev/urandom)' >&2; exit 1; }
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
host_key_sha256=$(sha256_of "$ssh_host_key")
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
  (cd "$home" && PORTLOOM_AGENT_IMAGE_ID="$recovery_image" run_compose --env-file .env -f compose.yml up -d --force-recreate --pull never) 9>&-
  progress 6 8 'Waiting for managed SSH and initial synchronization'
  if ! wait_ready "$recovery_image" "$ready_nonce"; then
    echo "Existing Agent was not ready after $ready_attempts attempts; fix connectivity and rerun the same command" >&2
    exit 1
  fi
  progress 7 8 'Removing the one-time enrollment credential'
  scrub_enrollment_token
  rotate_ready_nonce
  remove_ready "$recovery_image"
  (cd "$home" && PORTLOOM_AGENT_IMAGE_ID="$recovery_image" run_compose --env-file .env -f compose.yml up -d --force-recreate --pull never) 9>&-
  progress 8 8 'Verifying token-free restart and heartbeat'
  if ! wait_ready "$recovery_image" "$ready_nonce"; then
    echo 'Agent credentials were recovered, but SSH did not recover after removing the enrollment token; rerun after fixing connectivity' >&2
    exit 1
  fi
  install_success=true
  printf '\n%s PortLoom Agent recovery completed for %s.\n' "$(color '1;32' '✓')" "$name"
  printf '恢复完成：Agent %s 已重新连接。配置目录：%s\n' "$name" "$home"
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
(cd "$home" && PORTLOOM_AGENT_IMAGE_ID="$image" run_compose --env-file .env -f compose.yml up -d --pull never) 9>&-
progress 6 8 'Waiting for managed SSH and initial synchronization'
if ! wait_ready "$image" "$ready_nonce"; then
  echo "Agent was not ready after $ready_attempts attempts; its consumed credentials were saved and rerunning the same command will resume safely" >&2
  exit 1
fi
progress 7 8 'Removing the one-time enrollment credential'
scrub_enrollment_token
rotate_ready_nonce
remove_ready "$image"
(cd "$home" && PORTLOOM_AGENT_IMAGE_ID="$image" run_compose --env-file .env -f compose.yml up -d --force-recreate --pull never) 9>&-
progress 8 8 'Verifying token-free restart'
if ! wait_ready "$image" "$ready_nonce"; then
  echo 'Agent enrolled, but SSH did not recover after removing the enrollment token; rerun after fixing connectivity' >&2
  exit 1
fi
install_success=true
printf '\n%s PortLoom Agent is installed and enrolled as %s.\n' "$(color '1;32' '✓')" "$name"
printf '安装成功：Agent %s 已注册并连上服务器。现在到 WebUI 的 Routes 页面添加路由即可。\n' "$name"
printf 'Open the Server WebUI to add routes. Files: %s\n' "$home"
