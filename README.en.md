<div align="center">
  <img src="docs/public/logo.svg" width="92" alt="PortLoom logo" />
  <h1>PortLoom</h1>
  <p><strong>Weave reverse SSH tunnels into manageable, observable, rollback-friendly infrastructure.</strong></p>
  <p>A lightweight control plane for NAS, homelab, and small self-hosted environments.</p>
  <p><a href="README.md">简体中文</a> · <a href="README.en.md">English</a></p>
  <p>
    <a href="https://github.com/lkhmm520/portloom/actions/workflows/test.yml"><img alt="Tests" src="https://github.com/lkhmm520/portloom/actions/workflows/test.yml/badge.svg" /></a>
    <a href="https://github.com/lkhmm520/portloom/actions/workflows/docs.yml"><img alt="Docs" src="https://github.com/lkhmm520/portloom/actions/workflows/docs.yml/badge.svg" /></a>
    <a href="https://github.com/lkhmm520/portloom/pkgs/container/portloom-server"><img alt="GHCR" src="https://img.shields.io/badge/GHCR-server%20%7C%20agent%20%7C%20docs-0f9f72" /></a>
    <img alt="Go" src="https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white" />
  </p>
  <p><a href="https://docs.961121.xyz/"><strong>Documentation</strong></a> · <a href="#five-minute-start">Quick start</a> · <a href="https://github.com/lkhmm520/portloom/issues">Issues</a></p>
</div>

---

PortLoom keeps your existing Nginx Proxy Manager, certificates, DNS, and OpenSSH. It stores desired state, allocates safe loopback ports, coordinates NAS Agents running `ssh -R`, and exposes local, tunnel, and convergence health as separate layers.

## Why PortLoom

| Capability | Description |
| --- | --- |
| **Preserve ingress** | NPM keeps TLS; application domains share a Host Gateway |
| **Layered health** | Local service, SSH forwarding, and revisions are independent |
| **Least privilege** | Dedicated SSH user, pinned host key, loopback forwards, non-root read-only containers |
| **Safe enrollment** | One-time expiring tokens and per-Agent long-lived credentials |
| **Small footprint** | One Go Server, one Go Agent, SQLite, no external database |
| **Rollback first** | Run old and new tunnels in parallel and migrate one hostname at a time |

## Architecture

```text
Browser ── HTTPS ──> NPM ── Host ──> PortLoom Gateway :8081
                       │                      │
                       │ admin               ▼
                       └──────────> Server + SQLite
                                              ▲
                                              │ desired / observed
NAS service <── Agent <──── OpenSSH -R ───────┘
```

## Five-minute start

```bash
mkdir -p portloom/server && cd portloom/server
curl -LO https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/docker-compose.server.yml
curl -Lo server.env https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/server.env.example
mkdir -p data/server && sudo chown -R 65532:65532 data/server
openssl rand -hex 32  # use as TM_ADMIN_TOKEN
docker compose --env-file server.env -f docker-compose.server.yml up -d
curl --fail http://127.0.0.1:8080/healthz
```

Create a one-time enrollment token in the console, then deploy the NAS Agent from `examples/docker-compose.agent.yml`. Remove `TM_ENROLLMENT_TOKEN` after the Agent persists its identity. Follow the [production guide](https://docs.961121.xyz/en/install/production) for restricted SSH and reverse-proxy setup.

## Container images

| Component | Image |
| --- | --- |
| Server + Web console | `ghcr.io/lkhmm520/portloom-server:latest` |
| NAS Agent | `ghcr.io/lkhmm520/portloom-agent:latest` |
| Documentation site | `ghcr.io/lkhmm520/portloom-docs:latest` |

A strict stable `vX.Y.Z` Git tag publishes semantic-version, major/minor, `sha-*`, and `latest` tags. Prereleases never overwrite `latest`; manual runs publish only `edge` and `sha-*`. Pin exact versions in production.

## Documentation

> The official site is self-hosted through PortLoom. Set the repository variable `ENABLE_GITHUB_PAGES=true` to enable the optional GitHub Pages mirror.

Visit the [bilingual documentation portal](https://docs.961121.xyz/en/) for Docker installation, production hardening, route operations, configuration, API reference, backup, and troubleshooting.

Development preview:

```bash
npm ci
npm run docs:dev
```

Or build the static site and container:

```bash
npm ci
npm run docs:build
docker build -f Dockerfile.docs -t portloom-docs:local .
```

## Development

```bash
go mod download
npm ci
make check
make test-race
make build
make docker-build VERSION=local
```

## Current boundaries

- one Server writer; no active/active SQLite deployment;
- the Gateway proxies HTTP by Host; TCP routes are currently control-plane metadata;
- `tunnel_group` is metadata today. Use `examples/docker-compose.dual-agent.yml` for independent Web and media SSH master connections.

## Security

Never expose the administration listener or allocated SSH loopback ports directly to the Internet. Use a dedicated unprivileged SSH account, `GatewayPorts no`, read-only key mounts, and a verified `known_hosts` file. Never paste tokens, private keys, or full environment files into public issues.

## Contributing

Issues and pull requests are welcome. Run `make check`, `make test-race`, and `npm run docs:build` before submitting. Keep Chinese and English documentation structures aligned; repository `examples/` is the source of truth for downloadable Compose templates.

## License

No open-source license has been selected yet. Until one is chosen, the code remains copyrighted and requires permission for use, redistribution, or derivatives.
