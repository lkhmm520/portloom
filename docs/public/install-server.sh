#!/usr/bin/env bash
set -euo pipefail

usage() { cat <<'EOF'
Install a self-contained PortLoom Server on a public Docker host.

Usage: install-server.sh --domain portloom.example.com [options]
  --domain NAME       DNS name already pointing to this public host (required)
  --home PATH         Install directory (default: ~/.portloom/server)
  --web-port PORT     Loopback management port (default: 8080)
  --ssh-port PORT     Public managed SSH tunnel port (default: 2222)
  --version TAG       PortLoom image tag (default: latest)
  --migrate-native-edge
                      Explicitly migrate a legacy installer-managed Caddy deployment
  --enable-tcp-edge   Enable explicit public TCP route listeners (binds 0.0.0.0 by default)
                      Override with PORTLOOM_TCP_EDGE_BIND_HOST before running

PortLoom itself owns public TCP 80 and 443, obtains and renews ACME
certificates, and routes enabled HTTP hostnames. No Caddy/Nginx/NPM service is
installed. Point the management hostname and route hostnames (or one wildcard
record) at this host before opening them in a browser.
EOF
}
resolve_flock() {
  local candidate
  candidate=$(command -v flock 2>/dev/null || true)
  if [ -n "$candidate" ] && [ -x "$candidate" ]; then printf '%s' "$candidate"; return 0; fi
  for candidate in /opt/bin/flock /opt/sbin/flock; do
    if [ -x "$candidate" ]; then printf '%s' "$candidate"; return 0; fi
  done
  return 1
}

domain=""; home="${PORTLOOM_HOME:-$HOME/.portloom/server}"; web_port=8080; ssh_port=2222; version=latest
migrate_native_edge=false; enable_tcp_edge=false
tcp_edge_bind_host=""
edge_http_port=${PORTLOOM_HTTP_PORT:-80}; edge_https_port=${PORTLOOM_HTTPS_PORT:-443}
gateway_port=${PORTLOOM_GATEWAY_PORT:-8081}
edge_verify_attempts=${PORTLOOM_EDGE_VERIFY_ATTEMPTS:-30}
while [ "$#" -gt 0 ]; do
  case "$1" in
    --domain) domain=${2:-}; shift 2;; --home) home=${2:-}; shift 2;;
    --web-port) web_port=${2:-}; shift 2;; --ssh-port) ssh_port=${2:-}; shift 2;;
    --version) version=${2:-}; shift 2;; --migrate-native-edge) migrate_native_edge=true; shift;;
    --enable-tcp-edge) enable_tcp_edge=true; tcp_edge_bind_host=${PORTLOOM_TCP_EDGE_BIND_HOST:-0.0.0.0}; shift;;
    -h|--help) usage; exit 0;; *) echo "Unknown option: $1" >&2; usage >&2; exit 2;;
  esac
done
has_control() { local LC_ALL=C; [[ "$1" =~ [[:cntrl:]] ]]; }
valid_dns_name() {
  local value=$1 label
  [ -n "$value" ] && [ "${#value}" -le 253 ] && [[ "$value" != .* ]] && [[ "$value" != *. ]] || return 1
  [[ ! "$value" =~ ^[0-9]+(\.[0-9]+){3}$ ]] || return 1
  IFS=. read -r -a labels <<< "$value"
  [ "${#labels[@]}" -ge 2 ] || return 1
  [[ "${labels[${#labels[@]}-1]}" =~ [A-Za-z] ]] || return 1
  for label in "${labels[@]}"; do
    [ "${#label}" -le 63 ] && [[ "$label" =~ ^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$ ]] || return 1
  done
}
valid_dns_name "$domain" || { echo '--domain must be a valid DNS name' >&2; exit 2; }
if [ -z "$home" ] || has_control "$home"; then echo 'invalid --home: control characters are not allowed' >&2; exit 2; fi
[[ "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || { echo 'invalid --version' >&2; exit 2; }
for item in "$web_port" "$ssh_port" "$gateway_port" "$edge_http_port" "$edge_https_port"; do
  [[ "$item" =~ ^[0-9]+$ ]] && [ "$item" -ge 1 ] && [ "$item" -le 65535 ] || { echo 'ports must be within 1..65535' >&2; exit 2; }
done
[[ "$edge_verify_attempts" =~ ^[0-9]+$ ]] && [ "$edge_verify_attempts" -ge 1 ] && [ "$edge_verify_attempts" -le 120 ] || { echo 'PORTLOOM_EDGE_VERIFY_ATTEMPTS must be within 1..120' >&2; exit 2; }
ports=("$web_port" "$ssh_port" "$gateway_port" "$edge_http_port" "$edge_https_port")
for ((i=0; i<${#ports[@]}; i++)); do for ((j=i+1; j<${#ports[@]}; j++)); do
  [ "${ports[i]}" != "${ports[j]}" ] || { echo "port conflict: ${ports[i]} is assigned more than once" >&2; exit 2; }
done; done
command -v docker >/dev/null || { echo 'Docker is required' >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo 'Docker Compose v2 is required' >&2; exit 1; }
flock_bin=$(resolve_flock) || {
  echo 'flock is required to serialize Server installation' >&2
  echo 'QNAP/Entware: run /opt/bin/opkg install flock, then rerun this command.' >&2
  exit 1
}
umask 077
server_image="${PORTLOOM_SERVER_IMAGE_OVERRIDE:-ghcr.io/lkhmm520/portloom-server:$version}"
sshd_image="${PORTLOOM_SSHD_IMAGE_OVERRIDE:-ghcr.io/lkhmm520/portloom-sshd:$version}"
pull_server=true
pull_sshd=true
portloom_root="$HOME/.portloom"
case "$home" in "$portloom_root"|"$portloom_root"/*) mkdir -p "$portloom_root"; chmod 0711 "$portloom_root";; esac
mkdir -p "$home/server-data" "$home/ssh-auth" "$home/ssh-hostkeys"
home=$(cd "$home" && pwd -L)
chmod 0711 "$home"
exec 9>"$home/.install.lock"; chmod 0600 "$home/.install.lock"; "$flock_bin" -n 9 || { echo "another Server installation is already running in $home" >&2; exit 1; }
docker() { command docker "$@" 9>&-; }
chmod 0755 "$home/ssh-hostkeys"
chmod 0700 "$home/server-data" "$home/ssh-auth" 2>/dev/null || true
random_token() { if command -v openssl >/dev/null; then openssl rand -hex 32; else dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n'; fi; }
env_value() { local wanted=$1 key value; [ -f "$home/.env" ] || return 1; while IFS='=' read -r key value; do [ "$key" = "$wanted" ] && { printf '%s' "$value"; return 0; }; done < "$home/.env"; return 1; }
valid_image_id() { [[ "${1:-}" =~ ^sha256:[0-9a-f]{64}$ ]]; }
image_id_for_ref() {
  local resolved
  resolved=$(docker image inspect --format '{{.Id}}' "$1" 2>/dev/null) || return 1
  valid_image_id "$resolved" || return 1
  printf '%s' "$resolved"
}
resolve_existing_image_id() {
  local container=$1 service=$2 persisted=$3 identity running_id="" running_home running_service running_state trailing
  if [ -n "$persisted" ] && ! valid_image_id "$persisted"; then
    echo "persisted immutable image identity for $service is invalid" >&2
    return 1
  fi
  identity=$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}|{{ index .Config.Labels "com.docker.compose.service" }}|{{ .Image }}|{{ .State.Running }}' "$container" 2>/dev/null || true)
  if [ -n "$identity" ]; then
    IFS='|' read -r running_home running_service running_id running_state trailing <<< "$identity"
    if [ -n "$trailing" ] || [ "$running_home" != "$home" ] || [ "$running_service" != "$service" ] || ! valid_image_id "$running_id"; then
      echo "running $service identity does not match this install directory" >&2
      return 1
    fi
    if [ "$running_state" != true ]; then
      echo "running $service container is not active; start the original container or restore its immutable image ID before rerunning" >&2
      return 1
    fi
    if [ -n "$persisted" ] && [ "$persisted" != "$running_id" ]; then
      echo "running $service image does not match the persisted immutable image identity" >&2
      return 1
    fi
  fi
  [ -n "$running_id" ] || running_id=$persisted
  valid_image_id "$running_id" || { echo "immutable $service image identity is unavailable; restore the original container or image ID before rerunning" >&2; return 1; }
  printf '%s' "$running_id"
}
legacy_migration=false
native_upgrade=false
previous_server_image_id=""
previous_sshd_image_id=""
if [ -f "$home/.env" ]; then
  existing_server_image=$(env_value PORTLOOM_SERVER_IMAGE || true)
  existing_sshd_image=$(env_value PORTLOOM_SSHD_IMAGE || true)
  existing_server_image_id=$(env_value PORTLOOM_SERVER_IMAGE_ID || true)
  existing_sshd_image_id=$(env_value PORTLOOM_SSHD_IMAGE_ID || true)
  existing_http_port=$(env_value PORTLOOM_HTTP_PORT || true)
  existing_https_port=$(env_value PORTLOOM_HTTPS_PORT || true)
  existing_tcp_edge_bind_host=$(env_value PORTLOOM_TCP_EDGE_BIND_HOST || true)
  if [ -n "$existing_tcp_edge_bind_host" ]; then
    if [ "$enable_tcp_edge" = true ] && [ "$tcp_edge_bind_host" != "$existing_tcp_edge_bind_host" ]; then
      echo 'existing TCP Edge bind host does not match PORTLOOM_TCP_EDGE_BIND_HOST' >&2
      exit 1
    fi
    tcp_edge_bind_host=$existing_tcp_edge_bind_host
  fi
  legacy_http_port=$(env_value PORTLOOM_CADDY_HTTP_PORT || true)
  legacy_https_port=$(env_value PORTLOOM_CADDY_HTTPS_PORT || true)
  if [ -z "$existing_http_port" ] && [ -z "$existing_https_port" ] && [ -n "$legacy_http_port" ] && [ -n "$legacy_https_port" ]; then
    [ "$migrate_native_edge" = true ] || {
      echo 'legacy Caddy installation detected; rerun with --migrate-native-edge after reading the v0.3.0 backup and rollback guide' >&2
      exit 1
    }
    [ -f "$home/compose.yml" ] && [ -f "$home/Caddyfile" ] || {
      echo 'legacy Caddy configuration is incomplete; restore compose.yml and Caddyfile before migration' >&2
      exit 1
    }
    [ "$(env_value PORTLOOM_DOMAIN || true)" = "$domain" ] && [ "$(env_value PORTLOOM_WEB_PORT || true)" = "$web_port" ] &&
    [ "$(env_value PORTLOOM_SSH_PORT || true)" = "$ssh_port" ] && [ "$(env_value PORTLOOM_GATEWAY_PORT || true)" = "$gateway_port" ] &&
    [ "$legacy_http_port" = "$edge_http_port" ] && [ "$legacy_https_port" = "$edge_https_port" ] || {
      echo 'legacy Server configuration does not match this migration command; use the original domain and ports' >&2
      exit 1
    }
    [ "$server_image" != "$existing_server_image" ] || {
      echo 'legacy migration target must use a Server image reference different from the existing deployment; pass a pinned v0.3 --version' >&2
      exit 1
    }
    previous_server_image_id=$(resolve_existing_image_id portloom-server server "$existing_server_image_id") || exit 1
    previous_sshd_image_id=$(resolve_existing_image_id portloom-sshd sshd "$existing_sshd_image_id") || exit 1
    [ "$sshd_image" != "$existing_sshd_image" ] || pull_sshd=false
    backup_dir="$home/migration-backup-v0.3.0"
    backup_next="$home/.migration-backup-v0.3.0.next"
    [ ! -e "$backup_dir" ] && [ ! -e "$backup_next" ] || {
      echo "legacy migration backup already exists under $home; inspect or remove it before retrying" >&2
      exit 1
    }
    legacy_migration=true
  else
    [ "$migrate_native_edge" = false ] || {
      echo '--migrate-native-edge requires an installer-managed legacy Caddy configuration' >&2
      exit 1
    }
    [ "$(env_value PORTLOOM_DOMAIN || true)" = "$domain" ] && [ "$(env_value PORTLOOM_WEB_PORT || true)" = "$web_port" ] &&
    [ "$(env_value PORTLOOM_SSH_PORT || true)" = "$ssh_port" ] && [ "$(env_value PORTLOOM_GATEWAY_PORT || true)" = "$gateway_port" ] &&
    [ "$existing_http_port" = "$edge_http_port" ] && [ "$existing_https_port" = "$edge_https_port" ] || {
      echo 'existing Server configuration does not match this install command; use the original domain and ports or a new --home' >&2
      exit 1
    }
    previous_server_image_id=$(resolve_existing_image_id portloom-server server "$existing_server_image_id") || exit 1
    previous_sshd_image_id=$(resolve_existing_image_id portloom-sshd sshd "$existing_sshd_image_id") || exit 1
    [ "$existing_server_image" != "$server_image" ] || pull_server=false
    [ "$existing_sshd_image" != "$sshd_image" ] || pull_sshd=false
    tcp_config_change=false
    [ "$existing_tcp_edge_bind_host" = "$tcp_edge_bind_host" ] || tcp_config_change=true
    if [ "$pull_server" = true ] || [ "$pull_sshd" = true ] || [ "$tcp_config_change" = true ]; then
      native_upgrade=true
      backup_dir="$home/native-upgrade-backup-$version"
      backup_next="$home/.native-upgrade-backup-$version.next"
      [ ! -e "$backup_dir" ] && [ ! -e "$backup_next" ] || {
        echo "native upgrade backup already exists under $home; inspect or remove it before retrying" >&2
        exit 1
      }
    fi
  fi
elif [ "$migrate_native_edge" = true ]; then
  echo '--migrate-native-edge requires an existing legacy Caddy installation' >&2
  exit 1
fi
if [ "$legacy_migration" = true ] && [ "${PORTLOOM_NO_START:-false}" = true ]; then
  echo 'legacy Caddy migration cannot be combined with PORTLOOM_NO_START=true' >&2
  exit 1
fi
admin_token=$(env_value TM_ADMIN_TOKEN || true)
[[ "$admin_token" =~ ^[0-9A-Fa-f]{64}$ ]] || admin_token=$(random_token)
if [ "${PORTLOOM_SKIP_PULL:-false}" != true ]; then
  [ "$pull_server" = false ] || docker pull "$server_image" >/dev/null
  [ "$pull_sshd" = false ] || docker pull "$sshd_image" >/dev/null
fi
if [ "$pull_server" = false ] && [ -n "$previous_server_image_id" ]; then
  target_server_image_id=$previous_server_image_id
else
  target_server_image_id=$(image_id_for_ref "$server_image") || { echo "unable to resolve immutable Server image ID for $server_image" >&2; exit 1; }
fi
if [ "$pull_sshd" = false ] && [ -n "$previous_sshd_image_id" ]; then
  target_sshd_image_id=$previous_sshd_image_id
else
  target_sshd_image_id=$(image_id_for_ref "$sshd_image") || { echo "unable to resolve immutable SSHD image ID for $sshd_image" >&2; exit 1; }
fi
docker run --pull=never --rm --user 0:0 -v "$home/server-data:/server-data" -v "$home/ssh-auth:/ssh-auth" -v "$home/ssh-hostkeys:/ssh-hostkeys" --entrypoint /bin/sh "$target_server_image_id" -c 'umask 077; mkdir -p /server-data/certs; touch /ssh-auth/authorized_keys; chown -R 65532:65532 /server-data /ssh-auth; chmod 0700 /server-data /server-data/certs /ssh-auth; chmod 0600 /ssh-auth/authorized_keys; chown -R 0:0 /ssh-hostkeys; chmod 0755 /ssh-hostkeys'
restore_legacy_files() {
  local source=$1 file
  for file in .env compose.yml Caddyfile; do cp -p "$source/$file" "$home/$file.restore" || return 1; done
  for file in .env compose.yml Caddyfile; do mv "$home/$file.restore" "$home/$file" || return 1; done
  rm -rf "$source"
}
restore_native_files() {
  local source=$1 file
  for file in .env compose.yml; do cp -p "$source/$file" "$home/$file.restore" || return 1; done
  for file in .env compose.yml; do mv "$home/$file.restore" "$home/$file" || return 1; done
  rm -rf "$source"
}
verify_public_https() {
  local attempt
  for ((attempt=1; attempt<=edge_verify_attempts; attempt++)); do
    docker exec portloom-server curl --fail --silent --show-error --max-time 5 --resolve "$domain:$edge_https_port:127.0.0.1" "https://$domain:$edge_https_port/healthz" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}
printf '%s\n' \
  "PORTLOOM_SERVER_IMAGE=$server_image" "PORTLOOM_SERVER_IMAGE_ID=$target_server_image_id" \
  "PORTLOOM_SSHD_IMAGE=$sshd_image" "PORTLOOM_SSHD_IMAGE_ID=$target_sshd_image_id" "PORTLOOM_DOMAIN=$domain" \
  "PORTLOOM_WEB_PORT=$web_port" "PORTLOOM_SSH_PORT=$ssh_port" "PORTLOOM_GATEWAY_PORT=$gateway_port" \
  "PORTLOOM_HTTP_PORT=$edge_http_port" "PORTLOOM_HTTPS_PORT=$edge_https_port" \
  "PORTLOOM_TCP_EDGE_BIND_HOST=$tcp_edge_bind_host" "TM_ADMIN_TOKEN=$admin_token" > "$home/.env.next"
chmod 600 "$home/.env.next"
cat > "$home/compose.yml.next" <<'EOF'
name: portloom
services:
  sshd:
    image: ${PORTLOOM_SSHD_IMAGE_ID}
    container_name: portloom-sshd
    restart: unless-stopped
    network_mode: host
    environment:
      PORTLOOM_SSH_PORT: ${PORTLOOM_SSH_PORT}
    volumes:
      - ./ssh-hostkeys:/hostkeys
      - ./ssh-auth:/auth:ro
    read_only: true
    tmpfs:
      - /run:size=8m,mode=0755
      - /tmp:size=8m,mode=1777
    security_opt: [no-new-privileges:true]
    cap_drop: [ALL]
    cap_add: [SETUID, SETGID, SYS_CHROOT]
  server:
    image: ${PORTLOOM_SERVER_IMAGE_ID}
    container_name: portloom-server
    restart: unless-stopped
    network_mode: host
    depends_on:
      sshd:
        condition: service_healthy
    environment:
      TM_LISTEN_ADDR: 127.0.0.1:${PORTLOOM_WEB_PORT}
      TM_GATEWAY_ADDR: 127.0.0.1:${PORTLOOM_GATEWAY_PORT}
      TM_HEALTHCHECK_URL: http://127.0.0.1:${PORTLOOM_WEB_PORT}/healthz
      TM_EDGE_HTTP_ADDR: :${PORTLOOM_HTTP_PORT}
      TM_EDGE_HTTPS_ADDR: :${PORTLOOM_HTTPS_PORT}
      TM_TLS_CACHE_DIR: /data/certs
      TM_PUBLIC_HOST: ${PORTLOOM_DOMAIN}
      TM_DATABASE_PATH: /data/portloom.db
      TM_WEB_DIR: /app/web
      TM_ADMIN_TOKEN: ${TM_ADMIN_TOKEN}
      TM_AUTHORIZED_KEYS_PATH: /ssh-auth/authorized_keys
      TM_SSH_HOST_PUBLIC_KEY_PATH: /ssh-hostkeys/ssh_host_ed25519_key.pub
      TM_MANAGED_SSH_PORT: ${PORTLOOM_SSH_PORT}
      TM_MANAGED_SSH_ISOLATED: "true"
      TM_TCP_EDGE_BIND_HOST: ${PORTLOOM_TCP_EDGE_BIND_HOST}
    volumes:
      - ./server-data:/data
      - ./ssh-auth:/ssh-auth
      - ./ssh-hostkeys:/ssh-hostkeys:ro
    read_only: true
    tmpfs:
      - /tmp:size=16m,mode=1777
    # The non-root Server binary carries only cap_net_bind_service=ep.
    # no-new-privileges must stay disabled here or Linux suppresses that file capability.
    cap_drop: [ALL]
    cap_add: [NET_BIND_SERVICE]
EOF
chmod 600 "$home/compose.yml.next"
if [ "$legacy_migration" = true ] || [ "$native_upgrade" = true ]; then
  if ! mkdir -m 0700 "$backup_next"; then
    rm -f "$home/.env.next" "$home/compose.yml.next"
    exit 1
  fi
  backup_failed=false
  if [ "$legacy_migration" = true ]; then
    cp -p "$home/.env" "$home/compose.yml" "$home/Caddyfile" "$backup_next/" || backup_failed=true
  else
    cp -p "$home/.env" "$home/compose.yml" "$backup_next/" || backup_failed=true
  fi
  if [ "$backup_failed" = true ] || ! mv "$backup_next" "$backup_dir"; then
    rm -rf "$backup_next"
    rm -f "$home/.env.next" "$home/compose.yml.next"
    exit 1
  fi
fi
switch_failed=false
mv "$home/.env.next" "$home/.env" || switch_failed=true
if [ "$switch_failed" = false ]; then mv "$home/compose.yml.next" "$home/compose.yml" || switch_failed=true; fi
if [ "$switch_failed" = true ]; then
  rm -f "$home/.env.next" "$home/compose.yml.next"
  if [ "$legacy_migration" = true ]; then
    restore_legacy_files "$backup_dir" || echo 'failed to restore legacy files after configuration switch error; use the migration backup directory manually' >&2
  elif [ "$native_upgrade" = true ]; then
    restore_native_files "$backup_dir" || echo 'failed to restore native-edge files after configuration switch error; use the upgrade backup directory manually' >&2
  fi
  exit 1
fi
if [ "${PORTLOOM_NO_START:-false}" = true ]; then
  printf '\nPortLoom Server files were generated and initialized at %s.\n' "$home"
  exit 0
fi
activation_failed=false
activation_error='native-edge startup failed'
if [ "$legacy_migration" = true ]; then
  (cd "$home" && docker compose --project-directory "$home" --env-file migration-backup-v0.3.0/.env -f migration-backup-v0.3.0/compose.yml stop caddy) 9>&- || activation_failed=true
fi
if [ "$activation_failed" = false ]; then
  (cd "$home" && env -u PORTLOOM_DOMAIN -u PORTLOOM_WEB_PORT -u PORTLOOM_SSH_PORT -u PORTLOOM_GATEWAY_PORT -u PORTLOOM_HTTP_PORT -u PORTLOOM_HTTPS_PORT -u PORTLOOM_TCP_EDGE_BIND_HOST -u TM_ADMIN_TOKEN PORTLOOM_SERVER_IMAGE_ID="$target_server_image_id" PORTLOOM_SSHD_IMAGE_ID="$target_sshd_image_id" docker compose --env-file .env -f compose.yml up -d --remove-orphans --pull never) 9>&- || activation_failed=true
fi
if [ "$activation_failed" = false ] && ! verify_public_https; then
  activation_failed=true
  activation_error='native HTTPS edge did not become ready'
fi
if [ "$activation_failed" = true ]; then
  if [ "$legacy_migration" = true ]; then
    echo "$activation_error; restoring the legacy Caddy deployment" >&2
    rollback_failed=false
    (cd "$home" && docker compose --project-directory "$home" --env-file .env -f compose.yml down --remove-orphans) 9>&- || rollback_failed=true
    if ! restore_legacy_files "$backup_dir"; then
      rollback_failed=true
    elif ! (cd "$home" && PORTLOOM_SERVER_IMAGE="$previous_server_image_id" PORTLOOM_SSHD_IMAGE="$previous_sshd_image_id" docker compose --project-directory "$home" --env-file .env -f compose.yml up -d --pull never) 9>&-; then
      rollback_failed=true
    elif ! verify_public_https; then
      rollback_failed=true
    fi
    if [ "$rollback_failed" = true ]; then
      echo 'automatic legacy restoration could not be verified; canonical legacy files were restored when possible, but service status requires manual inspection' >&2
    fi
  elif [ "$native_upgrade" = true ]; then
    echo "$activation_error; restoring the previous native-edge deployment" >&2
    rollback_failed=false
    (cd "$home" && docker compose --project-directory "$home" --env-file .env -f compose.yml down --remove-orphans) 9>&- || rollback_failed=true
    if ! restore_native_files "$backup_dir"; then
      rollback_failed=true
    elif ! (cd "$home" && PORTLOOM_SERVER_IMAGE_ID="$previous_server_image_id" PORTLOOM_SSHD_IMAGE_ID="$previous_sshd_image_id" docker compose --project-directory "$home" --env-file .env -f compose.yml up -d --pull never) 9>&-; then
      rollback_failed=true
    elif ! verify_public_https; then
      rollback_failed=true
    fi
    if [ "$rollback_failed" = true ]; then
      echo 'automatic native-edge restoration could not be verified; canonical previous files were restored when possible, but service status requires manual inspection' >&2
    fi
  else
    echo "$activation_error" >&2
  fi
  exit 1
fi
printf '\nPortLoom Server is installed.\nWebUI: https://%s\nAdministrator token: %s\nFiles: %s\n\n' "$domain" "$admin_token" "$home"
printf 'Open TCP %s, %s and %s on the public-host firewall. Keep .env private.\n' "$edge_http_port" "$edge_https_port" "$ssh_port"
if [ -n "$tcp_edge_bind_host" ]; then
  printf 'TCP Edge is enabled on %s; open only the public ports explicitly configured in WebUI.\n' "$tcp_edge_bind_host"
else
  printf 'TCP Edge is disabled. Rerun this installer with --enable-tcp-edge to opt in.\n'
fi
