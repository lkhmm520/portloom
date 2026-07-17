# Production deployment

Choose the public ingress before deploying:

- Use the easy installer's Caddy when ports 80/443 are free.
- Deploy Server plus managed sshd behind an existing Caddy, Nginx, or NPM instance.
- Pin every image tag and audit the Compose files for regulated environments.

## Ports and traffic

| Port | Default bind | Purpose |
| --- | --- | --- |
| 80/443 | public Caddy address | WebUI and HTTP route hostnames |
| 2222 | public managed sshd | outbound Agent reverse tunnels |
| 8080 | 127.0.0.1 | Server WebUI and API |
| 8081 | 127.0.0.1 | Host-routing Gateway |
| 20000–29999 | 127.0.0.1 | allocated SSH loopback listeners |

The Agent network needs outbound access to Server ports 443 and 2222 only.

## Pin images

```bash
./install-server.sh --domain portloom.example.com --version 0.2.0
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml config
docker compose up -d
docker compose ps
```

Keep the Compose project name and volume paths during upgrades. Do not recreate the database service with ad hoc `docker run` commands.

## Managed SSH boundary

`portloom-sshd` is isolated from the host SSH service. It accepts Ed25519 public keys and remote forwarding to `127.0.0.1:*` only. It denies shell commands, TTY, X11, Agent forwarding, and user RC files.

Server writes the `ssh-auth` volume while sshd mounts it read-only. sshd writes persistent host keys while Server mounts them read-only. Server rebuilds `authorized_keys` from SQLite at startup.

Preserve `ssh-hostkeys/ssh_host_ed25519_key`. Replacing it correctly causes Agents to fail closed. Restore the original volume instead of disabling strict host-key checking.

## State and permissions

Back up `server-data/portloom.db`, `ssh-hostkeys/`, `.env`, `Caddyfile`, and `caddy-data/`. Server data and SSH authorization are written by UID/GID 65532. Do not solve permission errors with mode `0777`.

## Existing ingress

Remove Caddy from the easy Compose file. Keep Server on loopback or a firewall-protected private interface. Proxy the management hostname to 8080 and application hostnames to 8081 with the original Host header. See [Reverse proxy integration](/en/install/reverse-proxy). Managed sshd can remain on 2222 without changing host SSH on 22.

## Acceptance checks

Verify HTTPS WebUI login, rejected command login on 2222, Agent presence, local and tunnel health, public Host routing, automatic recovery after restarting Agent/Server/sshd, and a restore drill from backup.
