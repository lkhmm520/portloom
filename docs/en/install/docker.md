# Install with Docker

PortLoom uses two hosts: a public VPS runs Server, the WebUI, and managed sshd; the NAS/internal host runs Agent and initiates the outbound connection. One Server can manage multiple Agents.

## Before installation

| Item | Public VPS | NAS / internal host |
| --- | --- | --- |
| Docker | Docker Engine + Compose v2 | Docker; Agent accepts the Compose plugin or standalone `docker-compose` v2 |
| Network | Public IPv4; TCP 80/443/2222 by default | Outbound Server HTTPS and SSH only |
| Hostname | Your chosen complete management hostname points to the VPS | No hostname or public IP required |

The management hostname may be an apex domain or any subdomain; there is **no required `portloom.` prefix**. The default path also requires local VPS ports 80/443 to be free. If Caddy, Nginx, or NPM already owns them, read [Production deployment](/en/install/production) and [Reverse proxy integration](/en/install/reverse-proxy) first.

## Public VPS: choose one Server installation path

| Path | Best for | What you do | Characteristics |
| --- | --- | --- | --- |
| [Compose template](/en/guide/compose-install) | Compose users, NAS project UIs, inspectable files | Download `compose.yml` plus the env template, edit two values, and start | Familiar configuration and normal Compose operations |
| Secure installer | Automatic random credentials, image pinning, validation, and rollback | Download and run the installer | More automation and stronger upgrade guardrails |

### Option 1: Compose template

The dedicated page covers download through WebUI login:

- [Install with the Compose template](/en/guide/compose-install)
- [Download `compose.yml` directly](/examples/compose.yml)
- The Compose installation page provides the `.env` template and explains its two required values

The template exposes built-in public HTTPS, HTTP, and managed SSH by default. Server, sshd, and the one-shot state initializer all live in one conventional `compose.yml`; no reverse proxy is required first.

### Option 2: Secure installer

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
less install-server.sh
chmod 0700 install-server.sh
DOMAIN='example.com' # replace with your complete management hostname
./install-server.sh --domain "$DOMAIN" --version 0.4.1
```

The installer generates:

```text
compose.yml       Server + sshd Compose configuration
.env              immutable image IDs, ports, hostname, random admin token (0600)
server-data/      SQLite and certs/ cache
ssh-hostkeys/     Server SSH identity pinned by Agents
ssh-auth/         Agent authorization rebuilt from SQLite
```

The Server installer requires the Compose plugin and `flock`. It verifies the real HTTPS `/healthz` and restores the previous configuration on failed activation. Do not replace its generated project with ad-hoc `docker run` commands.

## NAS: add Agent from the WebUI

The Agent path is the same for both Server installation methods:

1. Open `https://your management hostname`.
2. Go to **Add Agent**.
3. Enter the Agent name, Server URL, VPS public SSH host, and port `2222`.
4. Select **Generate command**.
5. Paste the complete command on the NAS.

The Agent installer creates an Ed25519 key, pins the Server host key, enrolls with a one-time token, and removes that token after success. It handles common Synology/QNAP PATHs, multiple SHA-256 tools, and no-`flock` environments. Rerun the same command after fixing a failure; do not delete existing `~/.portloom/agent/data`.

Persist and back up the complete Agent directory (`.env`, `compose.yml`, and `data/`). `data/agent.json`, `data/ssh/id_ed25519`, and `data/ssh/known_hosts` are identity-critical.

## Inspect status

Compose-template path:

```bash
cd your-portloom-compose-directory
docker compose ps -a
docker compose logs --tail=100 server sshd
```

Installer default path:

```bash
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml ps
docker compose --env-file .env -f compose.yml logs --tail=100 server sshd
```

## Advanced ports and stream-edge settings

On first installer use, you can move the primary edge or disable TCP/UDP publication:

```bash
DOMAIN='example.com'

# Move the primary edge to local 8088/8443; public 80 must still reach 8088
./install-server.sh --domain "$DOMAIN" --version 0.4.1 \
  --http-port 8088 --https-port 8443

# Do not publish TCP/UDP on first install
./install-server.sh --domain "$DOMAIN" --version 0.4.1 \
  --disable-tcp-edge
```

For the Compose template, edit `TM_EDGE_HTTP_ADDR`, `TM_EDGE_HTTPS_ADDR`, and `TM_TCP_EDGE_BIND_HOST` in `.env`. An empty route `Public port` follows the primary edge. Do not enter the primary edge port again in a route; that requests an extra conflicting listener.

Allow TCP for custom web/TCP ports and UDP for UDP route ports. If the HTTP edge moves, public port 80 must still reach it or ACME HTTP-01 issuance fails.

## Data and upgrade boundaries

- Compose-template state defaults to `data/` in the project directory; installer state defaults to `~/.portloom/server/`.
- Never delete the Server database, certificate cache, SSH host keys, or Agent identity.
- The beginner template pins the verified Server/sshd `0.4.1` images and project-local `./data/` paths. Back up the complete project before explicitly changing both versions in `compose.yml`.
- Installer upgrades resolve immutable images and roll back automatically. For manual Compose upgrades, you own backup, tag changes, `pull`, `up -d`, and health verification.
- The current Agent installer does not support in-place cross-version changes. Do not force one by deleting keys or identity.

See [Configuration](/en/reference/configuration) for all environment variables and [Template downloads](/en/reference/templates) for advanced manual examples.
