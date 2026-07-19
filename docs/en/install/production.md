# Production deployment

## Choose public ingress first

| Mode | Use when | v0.4 capability |
| --- | --- | --- |
| Native edge (recommended) | Public 80 reaches the configured HTTP listener, and the configured HTTPS port is reachable externally at the **same advertised port** | Full HTTP/HTTPS, paths, extra web ports, management-host sub-paths, automatic certificates |
| External Caddy/Nginx/NPM | existing ingress must keep 80/443 | 8080 control + 8081 legacy Gateway; external TLS/redirects, no custom web `public_port` |

TCP/UDP stream edge is independent from native web edge and works in either mode unless `TM_TCP_EDGE_BIND_HOST` disables it.

## Port model

| Port | Default bind | Purpose |
| --- | --- | --- |
| 80/443 | public Server address | primary HTTP/HTTPS edges, ACME, WebUI, default web routes |
| dynamic web/TCP/UDP | host from `TM_EDGE_HTTP_ADDR` / `TM_TCP_EDGE_BIND_HOST` | v0.4 custom public routes |
| 2222 | public managed sshd | outbound Agent reverse tunnel |
| 8080/8081 | `127.0.0.1` | administration / legacy Gateway |
| 20000–29999 | loopback | allocated SSH forward for each route |

Public port 80 must reach the primary HTTP edge for HTTP-01. `--http-port 8088` changes only the local listener; externally forward `80 -> 8088`. HTTP 308 uses **Server's configured primary HTTPS port**, so configuration 8443 advertises public `:8443`. A lone `public 443 -> local 8443` mapping therefore does not make HTTP→HTTPS redirects complete: public 8443 must also reach that listener, the configured and public HTTPS ports must remain identical, or a front proxy must own TLS/redirects and rewrite Location correctly. Without such mapping/proxy behavior, custom HTTPS URLs must include the port.

Empty route `Public port` means the primary edge, not fixed 80/443. Keep it empty when the primary edge is 8088/8443; entering 8443 means an extra listener and is rejected because the primary edge reserves that port. Installer success output reports the actual WebUI URL and effective stream-edge state; still accept the deployment against `.env`, `/api/v1/system`, and end-to-end tests together.

## Pin and accept v0.4 images

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain portloom.example.com --version 0.4.0
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml config
docker compose --env-file .env -f compose.yml ps
docker compose --env-file .env -f compose.yml logs --tail=100 server
```

The installer already starts and verifies HTTPS. Do not immediately bypass protection with a bare `docker compose up`. Keep project name, install directory, and volume paths stable; do not recreate the database service with privileged or ad hoc containers.

Server runs non-root and needs only `NET_BIND_SERVICE` for low ports. Managed sshd denies shell, TTY, X11, Agent forwarding, and user RC; it accepts Ed25519 keys and loopback `-R` only.

## State and permissions

Back up at least:

```text
server-data/portloom.db (+ WAL/SHM when live)
server-data/certs/
ssh-hostkeys/
ssh-auth/
.env
compose.yml
```

`server-data` and `ssh-auth` are used by UID/GID 65532. Do not hide permission problems with `0777`, and do not back up only the main SQLite file.

## Acceptance

1. `/api/v1/system` reports `0.4.0` and expected `tcp_edge`, `udp_edge`, and `web_port_edge`.
2. HTTPS administration and HTTP 308 redirect work.
3. Agent restarts and heartbeats after the one-time token is removed.
4. Test HTTPS, HTTP, TCP, UDP, plus one path/custom-port route.
5. Inspect Local/Tunnel/Public, 60-minute traffic, and Server/Agent resources.
6. Rehearse restoration of database, certificate cache, and SSH host identity.
