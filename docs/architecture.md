---
search: false
---

# Architecture

## Purpose and constraints

PortLoom publishes selected NAS services through a small DMIT server while preserving the existing Nginx Proxy Manager (NPM) TLS and DNS setup. It uses OpenSSH remote forwarding rather than FRP. The design deliberately separates desired configuration, tunnel transport, and public TLS.

The control plane never needs the NPM API, DNS credentials, or access to the Docker socket. It does not start or configure the host SSH daemon.

## Components

```text
Browser
  │ HTTPS + admin Bearer token
  ▼
NPM ───────────────────────────────────────────────┐
  │ admin upstream                                 │ public HTTPS by Host
  ▼                                                ▼
PortLoom server :8080                 HTTP gateway :8081
  │ API + static UI                                │ lookup Host
  │                                                ▼
  ├── SQLite desired state                 127.0.0.1:<allocated port>
  │                                                ▲
  │ enrollment / heartbeat                         │ OpenSSH remote forward
  ▼                                                │
PortLoom agent ─────── ssh client ───────────┘
  │ local probe
  ▼
NAS service 127.0.0.1:<local port>
```

### Server

The server exposes two logical listeners:

1. **Admin listener** (`TM_LISTEN_ADDR`): Web UI, administration API, enrollment, and agent heartbeat endpoints.
2. **Gateway listener** (`TM_GATEWAY_ADDR`): resolves an HTTP `Host` to an enabled route and proxies to its allocated loopback port.

SQLite is the source of truth for clients, token hashes, route definitions, allocated ports, revisions, and observations. A single server process is expected for the MVP; SQLite and in-process reconciliation are not designed for active/active replicas.

### Agent

The agent runs on the NAS with host networking so route targets such as `127.0.0.1:8080` refer to the NAS. It enrolls once, persists its client credential under `/data`, then heartbeats to receive desired route revisions. Control-plane credentials require HTTPS by default; `TM_ALLOW_INSECURE_HTTP=true` permits HTTP only to `localhost`, `127.0.0.0/8`, or `::1` for local development. It probes local targets and reconciles grouped OpenSSH `-R` forwards without invoking a shell.

### OpenSSH transport

The host SSH daemon terminates SSH. A dedicated unprivileged account accepts the agent key. Remote forwards bind server loopback only. The agent image provides the SSH client and receives a private key and pinned `known_hosts` file as read-only mounts.

The server Compose deployment uses host networking because SSH loopback listeners created by the host daemon must be reachable by the gateway process. The agent uses host networking because it must reach NAS loopback services.

### Web UI

`web/index.html`, `web/assets/app.css`, and `web/assets/app.js` form a same-origin single-page application. There is no npm dependency, CDN request, transpilation, or bundling step. Dynamic values are written using `textContent`/DOM methods rather than interpolated HTML.

The admin token is kept in `sessionStorage`, not a persistent cookie or local storage. Each API request sends:

```http
Authorization: Bearer <admin-token>
```

A `401` or `403` response clears the session and returns to the login screen.

## State and revisions

Route edits advance a client's desired revision. A heartbeat reports the agent's observed revision and route observations. Desired and observed state remain separate, so a successful API write does not imply the tunnel has converged.

Each route displays three independent layers:

1. **Local service:** the agent can connect to the configured NAS host and port.
2. **SSH tunnel:** the OpenSSH process has established the remote forward.
3. **Public exposure:** the route is enabled and observed at its desired revision (and, for HTTP, is selectable by the gateway).

This prevents a green local probe from hiding a broken SSH session, or an active SSH process from hiding revision drift.

## Core flows

### Enrollment

1. An administrator authenticates to the Web UI and creates an expiring enrollment token.
2. The server returns the plaintext secret once and stores only the token verifier/hash.
3. The operator configures the secret on one agent.
4. The agent submits its stable name and token over HTTPS.
5. The server atomically consumes the token, creates the client, and returns a client credential.
6. The agent persists that credential; the operator removes the enrollment token from `.env`.

### Route change

1. The administrator creates or updates a route through the Bearer-protected API.
2. The server validates fields and allocates a unique loopback remote port.
3. The transaction stores the route and advances the desired revision.
4. The next heartbeat returns the new desired configuration.
5. The agent probes local services and reconciles OpenSSH forwards.
6. A later heartbeat reports observations and the applied revision.

### HTTP request

1. NPM terminates TLS and preserves the original `Host` header.
2. NPM sends HTTP to the gateway listener.
3. The gateway normalizes `Host`, finds one enabled HTTP route, and proxies to `127.0.0.1:<remote_port>`.
4. The host SSH daemon carries traffic through the reverse forward to the NAS target.

## Trust boundaries and controls

| Boundary | Control |
| --- | --- |
| Browser → admin API | HTTPS at NPM; long Bearer token; same-origin UI |
| Agent → control plane | HTTPS; one-time enrollment; per-client credential |
| Agent → SSH host | Public-key authentication; pinned host key; dedicated account |
| SSH host → gateway | Loopback-only remote forwards; allocated port range |
| Containers → host | Non-root user; read-only root; no capabilities; no Docker socket |
| Persistent state | Host-owned data directory; restricted permissions; SQLite backup |

Bearer tokens are credentials, not encryption. Never expose the admin listener over plaintext on an untrusted network. Keep NPM, the gateway, and the host SSH restrictions as independent defense layers.

## Failure behavior

- If the control plane is unavailable, a running agent leaves already-established forwards unchanged and retries on the configured polling interval. After an agent restart, the control plane must be reachable to restore desired routes.
- If an SSH operation fails, the agent reports tunnel failure and retries reconciliation on the next polling interval. It verifies the OpenSSH ControlMaster before trusting in-memory active routes and rebuilds forwards after a disconnected master.
- If canceling a forward fails, the agent reports the error and retains its previous observed revision until cancellation succeeds.
- If a local target is down, the tunnel may exist but the local layer remains unhealthy.
- If revisions differ, the public layer remains pending instead of reporting a false success.
- SQLite WAL files must be backed up consistently with the database; use SQLite's backup mechanism or stop the server before a file-level copy.
