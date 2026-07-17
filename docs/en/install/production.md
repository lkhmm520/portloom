# Production deployment

The quick start fits a VPS with free ports 80/443. Choose the public ingress before deploying:

- When 80/443 are free, use the easy installer and let PortLoom Server terminate HTTPS natively.
- With existing Caddy, Nginx, or NPM, use 8080/8081 as reverse-proxy upstreams; this is an advanced integration compatible with older deployments.
- For regulated environments, pin image tags and audit Compose, capabilities, and volume permissions.

## Native-edge ports and traffic

| Port | Default bind | Purpose |
| --- | --- | --- |
| 80/443 | public PortLoom Server address | ACME HTTP-01, HTTPS WebUI, and HTTP route hostnames |
| 2222 | public managed sshd | outbound Agent reverse tunnels |
| 8080 | 127.0.0.1 | internal Server WebUI and API listener |
| 8081 | 127.0.0.1 | internal Host-routing Gateway listener |
| 20000–29999 | 127.0.0.1 | allocated SSH loopback listeners |

The Agent network needs outbound access to Server ports 443 and 2222 only. Public port 80 must remain reachable for ACME HTTP-01 issuance and renewal.

Server requires the `NET_BIND_SERVICE` capability to bind 80/443 as a non-root user. The easy installer drops every capability and adds back only this one; do the same in hand-written Compose instead of using a privileged container.

## Pin images

```bash
./install-server.sh --domain portloom.example.com --version 0.3.0
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml config
docker compose ps
docker compose logs --tail=100 server
```

The installer has already started and verified the services; do not follow it with a bare `docker compose up`, which bypasses readiness and rollback protection. Keep the Compose project name and volume paths stable. Never recreate the database service with an ad hoc `docker run`.

## Managed SSH boundary

`portloom-sshd` is isolated from the host SSH service. It accepts Ed25519 public keys and remote forwarding to `127.0.0.1:*` only. It denies shell commands, TTY, X11, Agent forwarding, and user RC files.

Server writes the `ssh-auth` volume while sshd mounts it read-only. sshd writes persistent host keys while Server mounts them read-only. Server rebuilds `authorized_keys` from SQLite at startup. Preserve `ssh-hostkeys/ssh_host_ed25519_key`.

## State, certificates, and permissions

Back up:

```text
server-data/portloom.db
server-data/certs/
ssh-hostkeys/
.env
```

`/data/certs` is autocert's persistent certificate cache and maps to `server-data/certs/` in the default install. Server data and SSH authorization are written by UID/GID 65532. Do not solve permission errors with mode `0777`.

## Existing ingress (advanced/compatibility mode)

Leave native `TM_EDGE_HTTP_ADDR`/`TM_EDGE_HTTPS_ADDR` disabled, and keep Server's 8080/8081 listeners on loopback or a firewall-protected private interface. Proxy the management hostname to 8080 and application hostnames to 8081 with the original Host header. See [Reverse proxy integration](/en/install/reverse-proxy). Managed sshd can remain on 2222.

An external Caddy deployment that still uses on-demand TLS `ask` may enable the optional `TM_TLS_ASK_TOKEN` and `TM_TLS_ASK_ADDR` compatibility endpoint. It is not the certificate path used by the native edge.

## Acceptance checks

Verify HTTPS WebUI access, HTTP-to-HTTPS redirects, rejected command login on 2222, Agent/Local/Tunnel state, Host preservation, and restart recovery. Confirm only `TM_PUBLIC_HOST` and enabled HTTP route hostnames can trigger issuance, and test restoration of the database, certificate cache, and SSH host identity.
