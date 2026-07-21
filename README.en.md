<div align="center">
  <img src="docs/public/logo.svg" width="92" alt="PortLoom logo" />
  <h1>PortLoom</h1>
  <p><strong>Publish internal web, TCP, and UDP services with one generated Agent command.</strong></p>
  <p>A reverse-SSH tunnel control plane for NAS, homelab, and small self-hosted environments.</p>
  <p><a href="README.md">简体中文</a> · <a href="README.en.md">English</a></p>
  <p>
    <a href="https://github.com/lkhmm520/portloom/actions/workflows/test.yml"><img alt="Tests" src="https://github.com/lkhmm520/portloom/actions/workflows/test.yml/badge.svg" /></a>
    <a href="https://github.com/lkhmm520/portloom/actions/workflows/docs.yml"><img alt="Docs" src="https://github.com/lkhmm520/portloom/actions/workflows/docs.yml/badge.svg" /></a>
    <a href="https://github.com/lkhmm520/portloom/pkgs/container/portloom-server"><img alt="GHCR" src="https://img.shields.io/badge/GHCR-server%20%7C%20agent%20%7C%20sshd%20%7C%20docs-0f9f72" /></a>
    <img alt="Go" src="https://img.shields.io/badge/Go-1.25.12+-00ADD8?logo=go&logoColor=white" />
  </p>
  <p><a href="https://docs.look4i.com/en/"><strong>Documentation</strong></a> · <a href="#five-minute-start">Quick start</a> · <a href="https://github.com/lkhmm520/portloom/issues">Issues</a></p>
</div>

---

PortLoom's default path needs two Linux hosts with Docker Compose: a public VPS running Server and managed sshd, and a NAS or internal host running Agent. Server natively listens on public ports 80/443 and obtains certificates with autocert HTTP-01; the default path needs no Caddy, Nginx Proxy Manager, or host-OpenSSH changes. Install Server, generate one Agent command in the WebUI, then add HTTPS, HTTP, TCP, or UDP routes.

The built-in public ingress supports four route protocols: **HTTPS** (automatic certificates plus HTTP redirect), **HTTP** (plain-text publishing without a certificate), and **TCP**/**UDP** (dedicated public VPS ports; UDP is carried across the tunnel through a datagram relay). One domain can host multiple routes at the same time: split by path prefix (such as `example.com/jellyfin`), by custom public port (such as `example.com:8443`), or shared with the management domain via a path prefix.

## Architecture

```text
                               Public Docker host
Browser ──HTTP/HTTPS──> PortLoom web edge ──> Gateway ─────┐
TCP/UDP client ───────> PortLoom stream edge ──────────────┤
                               management host ──> WebUI/API│ loopback forwarding
                               authorization                ▼
                                    ▼                managed sshd :2222
                                                          ▲
                                                          │ Agent initiates OpenSSH -R
Internal HTTP/TCP/UDP service <──────────────────────── Agent
                                                NAS / internal Docker host
```

| Capability | Description |
| --- | --- |
| **Two-host setup** | Install the Server stack publicly and only Agent internally |
| **One Agent command** | The WebUI generates a shell-safe command with a one-time token, pinned host key, and matching version |
| **Built-in HTTPS** | Server uses autocert HTTP-01, authorizes only the management host and enabled HTTPS routes, and persists certificates in `/data/certs` |
| **Four protocols** | HTTPS / HTTP / TCP / UDP; plain HTTP is not forced to TLS, while TCP/UDP publish explicit public ports |
| **Endpoint reuse** | Reuse a hostname with path prefixes, custom public ports, and HTTP/HTTPS side by side |
| **Traffic and resources** | Dashboard shows a 60-minute series, totals, and Server/Agent CPU and memory; the metrics API also exposes per-route counters |
| **Managed SSH** | A separate sshd container permits public-key loopback remote forwarding only and leaves host sshd unchanged |
| **Layered status** | Local service, SSH tunnel, and public-listener state remain separate |
| **Small control plane** | Go Server, Go Agent, SQLite, no external database or Docker socket |

## Five-minute start

### Requirements

- Docker daemon access and Compose v2 on the public VPS and the NAS/internal host;
- your chosen complete management hostname pointing to the VPS; an apex domain or any subdomain works, with no required `portloom.` prefix;
- public VPS ports TCP `80`, `443`, and `2222`; ports `80/443` must be free. The NAS needs no inbound port.

### 1. Install Server on the public host

Start with the [conventional `compose.yml` template](https://docs.look4i.com/en/guide/compose-install): download `compose.yml` plus the env template, edit only the management hostname and administrator token, then use a Compose UI or run `docker compose up -d`. The PortLoom installer script is not required first.

If you prefer generated random credentials, immutable image resolution, HTTPS readiness, and failed-activation rollback, use the [secure installer](https://docs.look4i.com/en/install/docker#option-2-secure-installer) instead.

### 2. Add an Agent in the WebUI

Open `https://your management hostname` and sign in. Go to **Add Agent**, enter the Agent name, Server URL, public Server host, and SSH port, then select **Generate command**.

### 3. Run one command on the NAS

Paste the complete generated command on the NAS or internal Docker host. The Agent installer creates an Ed25519 key, pins the Server host key, enrolls with the one-time token, and starts Agent. The token is removed from configuration after enrollment succeeds.

### 4. Add an HTTPS route in the WebUI

Open **Routes → Add route**, select the Agent, keep the default **HTTPS** protocol, and enter the public hostname and local target:

| Field | Example |
| --- | --- |
| Name | Jellyfin |
| Client | home-nas |
| Protocol | HTTPS |
| Public domain | jellyfin.example.com |
| Local host | 127.0.0.1 |
| Local port | 8096 |

If wildcard DNS is not configured, point the application hostname to the VPS separately. Wait for Local, Tunnel, and Public state to converge, then open `https://jellyfin.example.com`.

See [Compose template installation](https://docs.look4i.com/en/guide/compose-install), the [five-minute quick start](https://docs.look4i.com/en/guide/quick-start), and [Docker installation](https://docs.look4i.com/en/install/docker) for details.

## Optional advanced integrations

The default installer includes Server's native HTTPS edge and managed sshd. Only deployments with an existing ingress or special compliance/network requirements need to:

- connect an existing Caddy, Nginx, or Nginx Proxy Manager instance to PortLoom upstreams `8080/8081`; this is a legacy/advanced integration, and external Caddy may optionally use the `/api/v1/tls/allow` and `TM_TLS_ASK_*` compatibility interface;
- omit managed sshd and use a hardened host OpenSSH service with a dedicated unprivileged account.

Neither is a prerequisite for a new installation. See [Production deployment](https://docs.look4i.com/en/install/production) and [Reverse proxy integration](https://docs.look4i.com/en/install/reverse-proxy).

## Container images

| Component | Image |
| --- | --- |
| Server + WebUI + HTTP Gateway | `ghcr.io/lkhmm520/portloom-server:latest` |
| Agent | `ghcr.io/lkhmm520/portloom-agent:latest` |
| Managed SSH service | `ghcr.io/lkhmm520/portloom-sshd:latest` |
| Documentation site | `ghcr.io/lkhmm520/portloom-docs:latest` |

A `vX.Y.Z` Git tag first publishes immutable exact-version images. After release acceptance passes, the finalize workflow promotes `latest`, major, and major/minor channels for stable versions and creates the GitHub Release. Prereleases do not promote stable channels. Pin exact versions in production. The WebUI passes the Server version to the Agent installer only when `/api/v1/system` returns a safe image tag.

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

Run `npm run docs:dev` for documentation development. The portals are [Chinese](https://docs.look4i.com/) and [English](https://docs.look4i.com/en/).

## Current boundaries

- one Server writer; no active/active SQLite deployment;
- the management edge ports can be moved with `TM_EDGE_HTTP_ADDR`/`TM_EDGE_HTTPS_ADDR` (or the installer's `--http-port/--https-port`); when port 80 moves, public port 80 must still reach the HTTP edge or ACME HTTP-01 issuance fails;
- UDP forwarding is encapsulated over the TCP tunnel (length-prefixed frames), suited to small/medium datagrams such as DNS or WireGuard handshakes; throughput is below native UDP;
- traffic and resource metrics are kept in memory and reset when the Server restarts;
- `tunnel_group` is metadata today; use multiple Agents/Clients for independent SSH master connections.

## Security

Never expose the administration listener, Gateway, or allocated SSH loopback ports directly to the Internet. Keep Agent connections outbound, pin the Server host key, bind reverse forwards to loopback, mount keys read-only, and retain minimum container privileges. Never paste tokens, private keys, or complete environment files into public issues.

## Contributing and release acceptance

Issues and pull requests are welcome. Run `make check`, `make test-race`, and `npm run docs:build` before submitting. Before the first public release is complete, the docs-hosted installer URLs and the `portloom-sshd` GHCR image must not be considered available release artifacts. After publishing, follow the [release acceptance checklist](https://docs.look4i.com/en/operations/release-checklist) to verify script downloads, all four images, version pinning, and the complete two-host flow.

## License

No open-source license has been selected yet. Until one is chosen, the code remains copyrighted and requires permission for use, redistribution, or derivatives.
