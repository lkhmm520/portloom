# Install with Docker

PortLoom uses two hosts: a public VPS runs Server plus managed sshd; the NAS/internal host runs Agent and initiates outbound connections.

## Requirements

- Docker daemon access and Compose v2 on both hosts. Agent accepts `docker compose` or standalone `docker-compose` v2; Server installer accepts only the Compose plugin;
- a management hostname pointing to the VPS;
- default install: public TCP 80/443/2222 and free local 80/443;
- the NAS needs only outbound Server HTTPS and SSH, with no inbound port.

If Caddy, Nginx, or NPM already owns 80/443, do not run the default command blindly. Read [Production deployment](/en/install/production) and [Reverse proxy integration](/en/install/reverse-proxy).

## Public host: Server v0.4

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
chmod 0700 install-server.sh
./install-server.sh \
  --domain portloom.example.com \
  --version 0.4.0
```

Server installer requires `flock`; only Agent installer has the directory-lock fallback for hosts without it. Generated layout:

```text
compose.yml       Server + sshd Compose configuration
.env              immutable image IDs, ports, host, random admin token (0600)
server-data/      SQLite and certs/ cache
ssh-hostkeys/     Server SSH identity pinned by Agents
ssh-auth/         Agent authorization rebuilt from SQLite
```

Never delete `server-data` or `ssh-hostkeys`, and do not reconstruct services with ad-hoc `docker run`. Upgrade an easy install by rerunning the newer installer so candidate generation, backup, real HTTPS readiness, and rollback remain transactional.

## v0.4 ports and stream-edge options

```bash
# Move primary edge to local 8088/8443; public 80 must still forward to 8088
./install-server.sh --domain portloom.example.com --version 0.4.0 \
  --http-port 8088 --https-port 8443

# Do not publish TCP/UDP on first install
./install-server.sh --domain portloom.example.com --version 0.4.0 \
  --disable-tcp-edge

# Restrict TCP/UDP publication to one IP on first install
PORTLOOM_TCP_EDGE_BIND_HOST=192.0.2.10 \
  ./install-server.sh --domain portloom.example.com --version 0.4.0 \
  --enable-tcp-edge
```

`--enable-tcp-edge` is compatibility-only; v0.4 binds `0.0.0.0` by default, but the installer reads a custom `PORTLOOM_TCP_EDGE_BIND_HOST` only when that flag is present. These options reliably define first install. A non-empty value in an existing `.env` is preserved, so rerun flags are not a general toggle.

Empty route `Public port` follows the primary edge. With the custom ports above, do not enter 8443 again in a route: that means an extra listener and conflicts with the primary port. HTTP 308 advertises public `:8443`; a lone `public 443 -> local 8443` mapping is insufficient for redirects. Also expose/map public 8443, keep configured and public ports identical, or let an outer proxy own and correctly rewrite redirects. Allow TCP for custom web/TCP ports and UDP for UDP route ports.

## Internal host: Agent

Copy the command from **Add Agent** instead of transcribing the one-time token. Persist and back up the complete `~/.portloom/agent/` install directory, including `.env`, `compose.yml`, and `data/`; `agent.json`, `ssh/id_ed25519`, and `ssh/known_hosts` inside `data/` are identity-critical.

The v0.4 installer adds common Synology/QNAP PATHs, checks Docker daemon access, accepts Compose plugin or standalone `docker-compose` v2, supports multiple SHA-256 tools, and falls back to a directory lock without `flock`. Rerun the same command after fixing a failure; never delete an existing identity. Remove a stale `$home/.install.lock.d` only after confirming no installer is running concurrently.

Rerunning the same command is only same-version resume/recovery. The current installer rejects changing an existing Agent directory to another `--version` and has no released installer-managed cross-version transaction. Do not force an upgrade by deleting `agent.json`/keys or blindly editing immutable image IDs.

## Inspect status

```bash
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml ps
docker compose --env-file .env -f compose.yml logs --tail=100 server
```

Pin `--version` in production. `latest` is for initial evaluation, not unattended upgrades. The installer writes resolved immutable image IDs into `.env`; rerunning the same reference does not silently follow a moved local tag.

## Build from source

```bash
git clone https://github.com/lkhmm520/portloom.git
cd portloom
make docker-build VERSION=local
```
