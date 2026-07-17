# Install with Docker

PortLoom is installed on two separate hosts. Do not install Server on the NAS or substitute Agent for Server on the VPS.

```text
Public VPS: Server (native HTTPS edge) + managed sshd
                 ▲
                 │ Agent initiates the encrypted tunnel
                 │
Private NAS: Agent → local service
```

## Requirements

Both hosts need Docker Engine 24+ and Compose v2. The public host also needs a management hostname pointing to it, public TCP 80/443/2222, and no existing process on 80/443 so Server can complete ACME HTTP-01 validation.

If the public host already runs Caddy, Nginx, or NPM, do not run the easy installer because ports will conflict. Follow [Production deployment](/en/install/production) and [Reverse proxy integration](/en/install/reverse-proxy), with the external ingress forwarding to 8080/8081.

## Public host: Server

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain portloom.example.com
```

The easy install runs only `portloom-server` and `portloom-sshd`. Server binds public ports 80/443 directly and uses autocert for certificates; Compose grants it `NET_BIND_SERVICE`. No Caddy, Nginx, or NPM service is installed.

The default directory is `~/.portloom/server`:

```text
compose.yml       Compose configuration for two services
.env              Images, ports, management hostname, and random token (mode 0600)
server-data/      SQLite database and certs/ certificate cache
ssh-hostkeys/     Persistent Server SSH identity
ssh-auth/         Agent authorization rebuilt by Server
```

The certificate cache is `/data/certs` inside the container and `server-data/certs/` on the host. Do not delete `server-data` or `ssh-hostkeys`: the first stores the database and ACME certificates; the second is the Server identity pinned by Agents.

When upgrading a v0.2.x easy install that includes Caddy, do not overwrite Compose directly. Follow the explicit `--migrate-native-edge` procedure in [Backup, upgrade, and rollback](/en/operations/backup-upgrade).

Inspect it with `docker compose ps` and `docker compose logs --tail=100` from `~/.portloom/server`. Do not upgrade an easy install with `docker compose pull && docker compose up -d`; that bypasses candidate configuration, backups, HTTPS readiness, and automatic rollback. Use the pinned new-`--version` installer rerun described below.

## Internal host: Agent

Copy the generated command from **Add Agent** in the WebUI. It calls the public `install-agent.sh` with one-time enrollment data. The default `~/.portloom/agent` directory persists identity in `data/agent.json` and SSH material in `data/ssh/`.

## Installer options

```bash
./install-server.sh --help
./install-agent.sh --help
```

Use `--version` to pin a release in production. `latest` is convenient for a first trial, not unattended upgrades. The installer persists the resolved immutable Server/sshd image IDs in `.env`, and Compose uses only those IDs; an idempotent rerun neither pulls an unchanged tag nor silently follows a locally moved `latest`. The first rerun of an older install migrates IDs from matching running containers; it fails closed when neither a container nor a persisted ID is available. To upgrade, pass a different pinned `--version`; the installer preserves the administrator token and persistent data and re-verifies HTTPS.

## Build from source

```bash
git clone https://github.com/lkhmm520/portloom.git
cd portloom
make docker-build VERSION=local
docker build -f Dockerfile.sshd -t portloom-sshd:local .
docker build -f Dockerfile.docs -t portloom-docs:local .
```
