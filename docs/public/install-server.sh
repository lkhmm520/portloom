#!/usr/bin/env bash
set -euo pipefail
usage() { cat <<'EOF'
Install PortLoom Server on the public Docker host.

Usage: install-server.sh --domain portloom.example.com [options]
  --domain NAME       DNS name already pointing to this public host (required)
  --home PATH         Install directory (default: ~/.portloom/server)
  --web-port PORT     Local Server port behind HTTPS (default: 8080)
  --ssh-port PORT     Public managed SSH tunnel port (default: 2222)
  --version TAG       PortLoom image tag (default: latest)
EOF
}
domain=""; home="${PORTLOOM_HOME:-$HOME/.portloom/server}"; web_port=8080; ssh_port=2222; version=latest
gateway_port=${PORTLOOM_GATEWAY_PORT:-8081}; tls_ask_port=${PORTLOOM_TLS_ASK_PORT:-8082}
caddy_http_port=${PORTLOOM_CADDY_HTTP_PORT:-80}; caddy_https_port=${PORTLOOM_CADDY_HTTPS_PORT:-443}
while [ "$#" -gt 0 ]; do
  case "$1" in
    --domain) domain=${2:-}; shift 2;; --home) home=${2:-}; shift 2;;
    --web-port) web_port=${2:-}; shift 2;; --ssh-port) ssh_port=${2:-}; shift 2;;
    --version) version=${2:-}; shift 2;; -h|--help) usage; exit 0;; *) echo "Unknown option: $1" >&2; usage >&2; exit 2;;
  esac
done
has_control() { local LC_ALL=C; [[ "$1" =~ [[:cntrl:]] ]]; }
[[ "$domain" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$ ]] || { echo '--domain must be a valid DNS name' >&2; exit 2; }
if [ -z "$home" ] || has_control "$home"; then
  echo 'invalid --home: control characters are not allowed' >&2
  exit 2
fi
[[ "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || { echo 'invalid --version' >&2; exit 2; }
for item in "$web_port" "$ssh_port" "$gateway_port" "$tls_ask_port" "$caddy_http_port" "$caddy_https_port"; do
  [[ "$item" =~ ^[0-9]+$ ]] && [ "$item" -ge 1 ] && [ "$item" -le 65535 ] || { echo 'ports must be within 1..65535' >&2; exit 2; }
done
ports=("$web_port" "$ssh_port" "$gateway_port" "$tls_ask_port" "$caddy_http_port" "$caddy_https_port")
for ((i=0; i<${#ports[@]}; i++)); do
  for ((j=i+1; j<${#ports[@]}; j++)); do
    [ "${ports[i]}" != "${ports[j]}" ] || { echo "port conflict: ${ports[i]} is assigned more than once" >&2; exit 2; }
  done
done
command -v docker >/dev/null || { echo 'Docker is required' >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo 'Docker Compose v2 is required' >&2; exit 1; }
umask 077
server_image="${PORTLOOM_SERVER_IMAGE_OVERRIDE:-ghcr.io/lkhmm520/portloom-server:$version}"
sshd_image="${PORTLOOM_SSHD_IMAGE_OVERRIDE:-ghcr.io/lkhmm520/portloom-sshd:$version}"
portloom_root="$HOME/.portloom"
case "$home" in
  "$portloom_root"|"$portloom_root"/*) mkdir -p "$portloom_root"; chmod 0711 "$portloom_root";;
esac
mkdir -p "$home/server-data" "$home/ssh-auth" "$home/ssh-hostkeys" "$home/caddy-data" "$home/caddy-config"
home=$(cd "$home" && pwd -L)
chmod 0711 "$home"
command -v flock >/dev/null || { echo 'flock is required to serialize Server installation' >&2; exit 1; }
exec 9>"$home/.install.lock"
chmod 0600 "$home/.install.lock"
flock -n 9 || { echo "another Server installation is already running in $home" >&2; exit 1; }
# Prevent long-running Docker children from keeping the installer lock after this shell exits.
docker() { command docker "$@" 9>&-; }
chmod 0700 "$home/caddy-data" "$home/caddy-config"
# Host keys remain root-owned; the directory must be traversable so the unprivileged Server can read the public key.
chmod 0755 "$home/ssh-hostkeys"
# These two directories are owned by the unprivileged Server UID after first run.
chmod 0700 "$home/server-data" "$home/ssh-auth" 2>/dev/null || true
random_token() {
  if command -v openssl >/dev/null; then openssl rand -hex 32
  else dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n'
  fi
}
env_value() {
  local wanted=$1 key value
  [ -f "$home/.env" ] || return 1
  while IFS='=' read -r key value; do
    if [ "$key" = "$wanted" ]; then printf '%s' "$value"; return 0; fi
  done < "$home/.env"
  return 1
}
if [ -f "$home/.env" ]; then
  [ "$(env_value PORTLOOM_SERVER_IMAGE || true)" = "$server_image" ] &&
    [ "$(env_value PORTLOOM_SSHD_IMAGE || true)" = "$sshd_image" ] &&
    [ "$(env_value PORTLOOM_DOMAIN || true)" = "$domain" ] &&
    [ "$(env_value PORTLOOM_WEB_PORT || true)" = "$web_port" ] &&
    [ "$(env_value PORTLOOM_SSH_PORT || true)" = "$ssh_port" ] &&
    [ "$(env_value PORTLOOM_GATEWAY_PORT || true)" = "$gateway_port" ] &&
    [ "$(env_value PORTLOOM_TLS_ASK_PORT || true)" = "$tls_ask_port" ] &&
    [ "$(env_value PORTLOOM_CADDY_HTTP_PORT || true)" = "$caddy_http_port" ] &&
    [ "$(env_value PORTLOOM_CADDY_HTTPS_PORT || true)" = "$caddy_https_port" ] || {
      echo 'existing Server configuration does not match this install command; use the original options or a new --home' >&2
      exit 1
    }
fi
admin_token=$(env_value TM_ADMIN_TOKEN || true)
tls_ask_token=$(env_value PORTLOOM_TLS_ASK_TOKEN || true)
[[ "$admin_token" =~ ^[0-9A-Fa-f]{64}$ ]] || admin_token=$(random_token)
[[ "$tls_ask_token" =~ ^[0-9A-Fa-f]{64}$ ]] || tls_ask_token=$(random_token)
printf '%s\n' \
  "PORTLOOM_SERVER_IMAGE=$server_image" "PORTLOOM_SSHD_IMAGE=$sshd_image" \
  "PORTLOOM_DOMAIN=$domain" "PORTLOOM_WEB_PORT=$web_port" "PORTLOOM_SSH_PORT=$ssh_port" \
  "PORTLOOM_GATEWAY_PORT=$gateway_port" "PORTLOOM_TLS_ASK_PORT=$tls_ask_port" \
  "PORTLOOM_CADDY_HTTP_PORT=$caddy_http_port" "PORTLOOM_CADDY_HTTPS_PORT=$caddy_https_port" \
  "TM_ADMIN_TOKEN=$admin_token" "PORTLOOM_TLS_ASK_TOKEN=$tls_ask_token" > "$home/.env.next"
chmod 600 "$home/.env.next"
mv "$home/.env.next" "$home/.env"
local_certs_line=""
[ "${PORTLOOM_CADDY_LOCAL_TLS:-false}" = true ] && local_certs_line="local_certs"
cat > "$home/Caddyfile.next" <<EOF
{
  http_port $caddy_http_port
  https_port $caddy_https_port
  $local_certs_line
  on_demand_tls {
    ask http://127.0.0.1:$tls_ask_port/api/v1/tls/allow?token={\$PORTLOOM_TLS_ASK_TOKEN}
  }
}
$domain {
  encode zstd gzip
  reverse_proxy 127.0.0.1:$web_port
}
https:// {
  tls {
    on_demand
  }
  encode zstd gzip
  reverse_proxy 127.0.0.1:$gateway_port
}
EOF
chmod 600 "$home/Caddyfile.next"
mv "$home/Caddyfile.next" "$home/Caddyfile"
cat > "$home/compose.yml.next" <<'EOF'
name: portloom
services:
  sshd:
    image: ${PORTLOOM_SSHD_IMAGE}
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
    image: ${PORTLOOM_SERVER_IMAGE}
    container_name: portloom-server
    restart: unless-stopped
    network_mode: host
    depends_on:
      sshd:
        condition: service_healthy
    environment:
      TM_LISTEN_ADDR: 127.0.0.1:${PORTLOOM_WEB_PORT}
      TM_GATEWAY_ADDR: 127.0.0.1:${PORTLOOM_GATEWAY_PORT}
      TM_DATABASE_PATH: /data/portloom.db
      TM_WEB_DIR: /app/web
      TM_ADMIN_TOKEN: ${TM_ADMIN_TOKEN}
      TM_AUTHORIZED_KEYS_PATH: /ssh-auth/authorized_keys
      TM_SSH_HOST_PUBLIC_KEY_PATH: /ssh-hostkeys/ssh_host_ed25519_key.pub
      TM_MANAGED_SSH_PORT: ${PORTLOOM_SSH_PORT}
      TM_MANAGED_SSH_ISOLATED: "true"
      TM_PUBLIC_HOST: ${PORTLOOM_DOMAIN}
      TM_TLS_ASK_TOKEN: ${PORTLOOM_TLS_ASK_TOKEN}
      TM_TLS_ASK_ADDR: 127.0.0.1:${PORTLOOM_TLS_ASK_PORT}
    volumes:
      - ./server-data:/data
      - ./ssh-auth:/ssh-auth
      - ./ssh-hostkeys:/ssh-hostkeys:ro
    read_only: true
    tmpfs:
      - /tmp:size=16m,mode=1777
    security_opt: [no-new-privileges:true]
    cap_drop: [ALL]
  caddy:
    image: caddy:2-alpine
    container_name: portloom-caddy
    restart: unless-stopped
    network_mode: host
    depends_on: [server]
    environment:
      PORTLOOM_TLS_ASK_TOKEN: ${PORTLOOM_TLS_ASK_TOKEN}
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./caddy-data:/data
      - ./caddy-config:/config
EOF
chmod 600 "$home/compose.yml.next"
mv "$home/compose.yml.next" "$home/compose.yml"
if [ "${PORTLOOM_SKIP_PULL:-false}" != true ]; then
  docker pull "$server_image" >/dev/null
  docker pull "$sshd_image" >/dev/null
fi
docker run --rm --user 0:0 -v "$home/server-data:/server-data" -v "$home/ssh-auth:/ssh-auth" -v "$home/ssh-hostkeys:/ssh-hostkeys" \
  --entrypoint /bin/sh "$server_image" -c \
  'umask 077; touch /ssh-auth/authorized_keys; chown -R 65532:65532 /server-data /ssh-auth; chmod 0700 /server-data /ssh-auth; chmod 0600 /ssh-auth/authorized_keys; chown -R 0:0 /ssh-hostkeys; chmod 0755 /ssh-hostkeys'
if [ "${PORTLOOM_NO_START:-false}" = true ]; then
  printf '\nPortLoom Server files were generated and initialized at %s.\n' "$home"
  exit 0
fi
(cd "$home" && env -u PORTLOOM_DOMAIN -u PORTLOOM_WEB_PORT -u PORTLOOM_SSH_PORT -u PORTLOOM_GATEWAY_PORT -u PORTLOOM_TLS_ASK_PORT -u PORTLOOM_CADDY_HTTP_PORT -u PORTLOOM_CADDY_HTTPS_PORT -u TM_ADMIN_TOKEN -u PORTLOOM_TLS_ASK_TOKEN PORTLOOM_SERVER_IMAGE="$server_image" PORTLOOM_SSHD_IMAGE="$sshd_image" docker compose --env-file .env -f compose.yml up -d) 9>&-
printf '\nPortLoom Server is installed.\nWebUI: https://%s\nAdministrator token: %s\nFiles: %s\n\n' "$domain" "$admin_token" "$home"
printf 'Open TCP %s, %s and %s on the public-host firewall. Keep .env private.\n' "$caddy_http_port" "$caddy_https_port" "$ssh_port"
