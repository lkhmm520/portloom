# PortLoom

PortLoom is a small control plane for publishing services from a NAS through safe OpenSSH reverse tunnels. A Go server stores desired configuration in SQLite, a Go agent reconciles tunnels on the NAS, and a dependency-free Web UI manages clients, one-time enrollment tokens, and routes.

TLS and public DNS stay at the existing reverse proxy. PortLoom does not replace Nginx Proxy Manager (NPM), run an SSH daemon, or modify DNS.

## Features

- Bearer-token protected administration API and single-page Web console
- One-time, expiring agent enrollment tokens
- Client heartbeat and desired/observed revision tracking
- HTTP routes selected by `Host` and TCP route metadata
- Collision-free server loopback port allocation
- Layered route health: local service, SSH tunnel, and public exposure
- SQLite persistence with no external database
- Multi-stage server and agent images; no JavaScript build pipeline

## Repository layout

```text
cmd/server/              Server entry point
cmd/agent/               NAS agent entry point
internal/                Domain, storage, API, gateway, and SSH reconciliation
web/                     Plain HTML, CSS, and JavaScript admin console
Dockerfile.server        Server container image
Dockerfile.agent         Agent container image with OpenSSH client
deploy/server/           Server Compose deployment
deploy/agent/            Agent Compose deployment
docs/architecture.md     Components, flows, and trust boundaries
docs/deployment.md       Production deployment and operations
```

## Quick start for development

Requirements: Go 1.24+, Node.js (syntax-check only), and optionally Docker.

```sh
go mod download
make test
make web-check
make build
```

Run the server with explicit local-only settings:

```sh
export TM_ADMIN_TOKEN="$(openssl rand -hex 32)"
export TM_LISTEN_ADDR=127.0.0.1:8080
export TM_GATEWAY_ADDR=127.0.0.1:8081
export TM_DATABASE_PATH="$(pwd)/data/portloom.db"
export TM_WEB_DIR="$(pwd)/web"
./bin/portloom-server
```

Open `http://127.0.0.1:8080/` and sign in with `TM_ADMIN_TOKEN`. The token is held in `sessionStorage`, sent in the HTTP Authorization header using the Bearer scheme, and removed when the tab session ends or the user signs out.

## Web console

The files under `web/` use browser APIs directly and require no package manager, bundler, CDN, or generated artifacts. The console provides:

- **Dashboard:** online clients, enabled routes, tunnel health, revision drift
- **Clients:** enrollment identity, connection state, version, and last heartbeat
- **Enrollment tokens:** create expiring, one-time credentials and copy the secret once
- **Routes:** create, inspect, edit, and delete HTTP or TCP routes
- **Layered status:** separate local reachability, tunnel state, and public convergence

The UI calls the same-origin `/api/v1` endpoints. Put the admin UI and API behind HTTPS before exposing them beyond loopback.

## Containers

Build both images:

```sh
make docker-build VERSION=local
```

The server image contains the static Web assets. The agent image contains `ssh` but no SSH private key; keys and known-host records are mounted read-only at runtime. Both images run as UID/GID `65532`, drop Linux capabilities in Compose, and use a read-only root filesystem.

See [docs/deployment.md](docs/deployment.md) for complete server, NPM, SSH account, and agent instructions. See [docs/architecture.md](docs/architecture.md) for the data flow and security model.

## Configuration

Copy `.env.example` to `.env` on each host and fill only the relevant server or agent section. Important values:

| Variable | Purpose |
| --- | --- |
| `TM_ADMIN_TOKEN` | Long random secret accepted by the admin API |
| `TM_LISTEN_ADDR` | Admin API/UI listener; keep on loopback behind HTTPS |
| `TM_GATEWAY_ADDR` | Host-routing gateway listener used by NPM |
| `TM_DATABASE_PATH` | SQLite database path inside the server container |
| `TM_SERVER_URL` | HTTPS control-plane URL used by the agent |
| `TM_ALLOW_INSECURE_HTTP` | Local-development opt-in; permits HTTP only for `localhost`, `127.0.0.0/8`, or `::1` |
| `TM_ENROLLMENT_TOKEN` | One-time token used only for first enrollment |
| `TM_CLIENT_NAME` | Stable human-readable NAS identity |
| `TM_SSH_*` | Dedicated SSH endpoint, account, key, and host verification |

After successful enrollment, remove `TM_ENROLLMENT_TOKEN` from `.env`; the persisted agent credential is used for later heartbeats.

## Quality checks

```sh
make fmt-check  # gofmt verification
make vet        # static analysis
make test       # unit and integration tests
make test-race  # race detector
make web-check  # JavaScript syntax and required asset checks
make check      # all non-race checks
```

GitHub Actions runs formatting, vet, tests with the race detector, binary builds, Web checks, Compose rendering, and both Docker builds.

## License

No license has been selected yet.
