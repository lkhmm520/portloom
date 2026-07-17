# Install with Docker

PortLoom is installed on two separate hosts. Do not install Server on the NAS or substitute Agent for Server on the VPS.

```text
Public VPS: Server + managed sshd + Caddy
                 ▲
                 │ Agent initiates the encrypted tunnel
                 │
Private NAS: Agent → local service
```

## Requirements

Both hosts need Docker Engine 24+ and Compose v2. The public host also needs a management hostname pointing to it, public TCP 80/443/2222, and no existing process on 80/443.

If the public host already runs Caddy, Nginx, or NPM, do not run the easy installer because ports will conflict. Follow [Production deployment](/en/install/production) and [Reverse proxy integration](/en/install/reverse-proxy).

## Public host: Server

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain portloom.example.com
```

The default directory is `~/.portloom/server`:

```text
compose.yml       Compose configuration for all three services
.env              Images, ports, and random tokens (mode 0600)
server-data/      SQLite database
ssh-hostkeys/     Persistent Server SSH identity
ssh-auth/         Agent authorization rebuilt by Server
caddy-data/       Certificates and Caddy state
```

Operate it with:

```bash
cd ~/.portloom/server
docker compose ps
docker compose logs --tail=100
docker compose pull
docker compose up -d
```

Do not delete `server-data` or `ssh-hostkeys`. The first stores configuration; the second is the Server identity pinned by Agents.

## Internal host: Agent

Copy the generated command from **Add Agent** in the WebUI. It calls the public `install-agent.sh` with one-time enrollment data.

The default directory is `~/.portloom/agent`:

```text
compose.yml       Agent Compose configuration
.env              Server and SSH addresses; no enrollment token after success
data/agent.json   Persistent Agent identity
data/ssh/         Agent private key and pinned Server host key
```

Operate it with:

```bash
cd ~/.portloom/agent
docker compose ps
docker compose logs --tail=100
docker compose pull
docker compose up -d
```

## Installer options

```bash
./install-server.sh --help
./install-agent.sh --help
```

Use `--version` to pin a release in production. `latest` is convenient for a first trial, not unattended upgrades.

## Build from source

```bash
git clone https://github.com/lkhmm520/portloom.git
cd portloom
make docker-build VERSION=local
docker build -f Dockerfile.sshd -t portloom-sshd:local .
docker build -f Dockerfile.docs -t portloom-docs:local .
```
