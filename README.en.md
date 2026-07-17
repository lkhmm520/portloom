<div align="center">
  <img src="docs/public/logo.svg" width="92" alt="PortLoom logo" />
  <h1>PortLoom</h1>
  <p><strong>Publish internal HTTP/HTTPS services safely with one generated Agent command.</strong></p>
  <p>A reverse-SSH tunnel control plane for NAS, homelab, and small self-hosted environments.</p>
  <p><a href="README.md">简体中文</a> · <a href="README.en.md">English</a></p>
  <p>
    <a href="https://github.com/lkhmm520/portloom/actions/workflows/test.yml"><img alt="Tests" src="https://github.com/lkhmm520/portloom/actions/workflows/test.yml/badge.svg" /></a>
    <a href="https://github.com/lkhmm520/portloom/actions/workflows/docs.yml"><img alt="Docs" src="https://github.com/lkhmm520/portloom/actions/workflows/docs.yml/badge.svg" /></a>
    <a href="https://github.com/lkhmm520/portloom/pkgs/container/portloom-server"><img alt="GHCR" src="https://img.shields.io/badge/GHCR-server%20%7C%20agent%20%7C%20sshd%20%7C%20docs-0f9f72" /></a>
    <img alt="Go" src="https://img.shields.io/badge/Go-1.25.12+-00ADD8?logo=go&logoColor=white" />
  </p>
  <p><a href="https://docs.961121.xyz/en/"><strong>Documentation</strong></a> · <a href="#five-minute-start">Quick start</a> · <a href="https://github.com/lkhmm520/portloom/issues">Issues</a></p>
</div>

---

PortLoom's default path needs two Linux hosts with Docker Compose: a public VPS running Server, managed sshd, and Caddy; and a NAS or internal host running Agent. Install Server, generate one Agent command in the WebUI, then add an HTTP route in the WebUI. The default path does not require Nginx Proxy Manager or changes to host OpenSSH.

The built-in public ingress fully supports hostname-based **HTTP/HTTPS** publishing. TCP fields remain compatibility metadata only: they do not create a public TCP listener and are never shown as published/healthy in the WebUI.

## Architecture

```text
                         Public Docker host
Browser ──HTTPS──> Caddy ──Host──> PortLoom Gateway
                    │                  │
                    │ management host  ▼
                    └──────────> Server + SQLite
                                         │ authorization
                                         ▼
                                  managed sshd :2222
                                         ▲
                                         │ Agent initiates OpenSSH -R
                                         │
Internal HTTP service <── Agent <───────┘
                         NAS / internal Docker host
```

| Capability | Description |
| --- | --- |
| **Two-host setup** | Install the Server stack publicly and only Agent internally |
| **One Agent command** | The WebUI generates a shell-safe command with a one-time token, pinned host key, and matching version |
| **Built-in HTTPS** | Caddy obtains certificates for the management host and enabled HTTP routes |
| **Managed SSH** | A separate sshd container permits public-key loopback remote forwarding only and leaves host sshd unchanged |
| **Layered status** | Local service, SSH tunnel, and HTTP public-publishing state remain separate |
| **Small control plane** | Go Server, Go Agent, SQLite, no external database or Docker socket |

## Five-minute start

### Requirements

- Docker Engine 24+ and Compose v2 on the public VPS and the NAS/internal host;
- a management hostname such as `portloom.example.com` pointing to the VPS;
- public VPS ports TCP `80`, `443`, and `2222`; ports `80/443` must be free. The NAS needs no inbound port.

### 1. Install Server on the public host

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
less install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain portloom.example.com
```

The installer starts `portloom-server`, `portloom-sshd`, and `portloom-caddy`, then prints the WebUI URL and a random administrator token.

### 2. Add an Agent in the WebUI

Open `https://portloom.example.com` and sign in. Go to **Add Agent**, enter the Agent name, Server URL, public Server host, and SSH port, then select **Generate command**.

### 3. Run one command on the NAS

Paste the complete generated command on the NAS or internal Docker host. The Agent installer creates an Ed25519 key, pins the Server host key, enrolls with the one-time token, and starts Agent. The token is removed from configuration after enrollment succeeds.

### 4. Add an HTTP route in the WebUI

Open **Routes → Add HTTP route**, select the Agent, and enter the public hostname and local target:

| Field | Example |
| --- | --- |
| Name | Jellyfin |
| Client | home-nas |
| Public domain | jellyfin.example.com |
| Local host | 127.0.0.1 |
| Local port | 8096 |

If wildcard DNS is not configured, point the application hostname to the VPS separately. Wait for Local, Tunnel, and Public state to converge, then open `https://jellyfin.example.com`.

See the [five-minute quick start](https://docs.961121.xyz/en/guide/quick-start) and [Docker installation](https://docs.961121.xyz/en/install/docker) for details.

## Optional advanced integrations

The default installer includes Caddy and managed sshd. Only deployments with an existing ingress or special compliance/network requirements need to:

- connect an existing Caddy, Nginx, or Nginx Proxy Manager instance to PortLoom upstreams `8080/8081`;
- omit managed sshd and use a hardened host OpenSSH service with a dedicated unprivileged account.

Neither is a prerequisite for a new installation. See [Production deployment](https://docs.961121.xyz/en/install/production) and [Reverse proxy integration](https://docs.961121.xyz/en/install/reverse-proxy).

## Container images

| Component | Image |
| --- | --- |
| Server + WebUI + HTTP Gateway | `ghcr.io/lkhmm520/portloom-server:latest` |
| Agent | `ghcr.io/lkhmm520/portloom-agent:latest` |
| Managed SSH service | `ghcr.io/lkhmm520/portloom-sshd:latest` |
| Documentation site | `ghcr.io/lkhmm520/portloom-docs:latest` |

A stable `vX.Y.Z` Git tag publishes exact semantic-version, major/minor, `sha-*`, and `latest` tags. Prereleases do not overwrite `latest`; manual runs publish only `edge` and `sha-*`. Pin exact versions in production. The WebUI passes the Server version to the Agent installer only when `/api/v1/system` returns a safe image tag.

## Run Server from source

```bash
make build
export TM_ADMIN_TOKEN="$(openssl rand -hex 32)"
export TM_LISTEN_ADDR=127.0.0.1:8080
export TM_GATEWAY_ADDR=127.0.0.1:8081
export TM_DATABASE_PATH="$(pwd)/data/portloom.db"
export TM_WEB_DIR="$(pwd)/web"
mkdir -p "$(pwd)/data"
./bin/portloom-server
```

The WebUI keeps the administrator token in tab-scoped `sessionStorage` and sends it in the **HTTP Authorization header using the Bearer scheme**. Signing out or ending the session clears it.

## Development and documentation

Go 1.25.12+ and Node.js 20+ are required; Docker is used for images and end-to-end verification:

```bash
go mod download
npm ci
make check
make test-race
make build
npm run docs:build
```

Run `npm run docs:dev` for documentation development. The portals are [Chinese](https://docs.961121.xyz/) and [English](https://docs.961121.xyz/en/).

## Current boundaries

- one Server writer; no active/active SQLite deployment;
- the built-in public ingress fully supports HTTP/HTTPS Host routes only; existing TCP records are control-plane metadata;
- `tunnel_group` is metadata today; use multiple Agents/Clients for independent SSH master connections.

## Security

Never expose the administration listener, Gateway, or allocated SSH loopback ports directly to the Internet. Keep Agent connections outbound, pin the Server host key, bind reverse forwards to loopback, mount keys read-only, and retain minimum container privileges. Never paste tokens, private keys, or complete environment files into public issues.

## Contributing and release acceptance

Issues and pull requests are welcome. Run `make check`, `make test-race`, and `npm run docs:build` before submitting. Before the first public release is complete, the docs-hosted installer URLs and the `portloom-sshd` GHCR image must not be considered available release artifacts. After publishing, follow the [release acceptance checklist](https://docs.961121.xyz/en/operations/release-checklist) to verify script downloads, all four images, version pinning, and the complete two-host flow.

## License

No open-source license has been selected yet. Until one is chosen, the code remains copyrighted and requires permission for use, redistribution, or derivatives.
