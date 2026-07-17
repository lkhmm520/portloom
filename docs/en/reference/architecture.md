# Architecture

```text
                             Public Docker host
Browser ──HTTPS──> Caddy/existing ingress ──Host──> Gateway :8081
                            │                         │
                            │ management host         ▼
                            └────────────────> Server / SQLite
                                                       │ authorization volume
                                                       ▼
                                                  managed sshd :2222
                                                       ▲
                                                       │ Agent initiates OpenSSH -R
                                                       │
Internal service <──────────── Agent <─────────────────┘
```

Server stores Agents, token verifiers, SSH public keys, routes, allocated ports, and observations in SQLite. The WebUI uses the admin API; Agents enroll, sync, and heartbeat over HTTPS. One Server writer owns the database.

Each Agent keeps one OpenSSH ControlMaster and applies route changes with `ssh -O forward/cancel -R`. Managed sshd permits loopback remote forwarding only.

Ingress terminates TLS and preserves Host. Gateway selects an enabled, converged HTTP route and proxies to its VPS loopback port. OpenSSH carries the connection to the Agent and its configured local target.

The easy Caddy deployment uses a random-token-protected local ask endpoint. It obtains certificates only for the management hostname and enabled HTTP routes.

Running Agents preserve existing forwards during a brief Server outage. Agents rebuild a failed SSH master. Host-key changes fail closed. Server rebuilds Agent authorization from SQLite at startup, and failed forward cancellation never reports false convergence.
