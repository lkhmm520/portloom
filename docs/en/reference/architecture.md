# Architecture

```text
                              Public Docker host
Browser ──HTTP/HTTPS──> PortLoom Server native edge :80/:443
                                  │
         management host ─────────┼──> WebUI / API / SQLite
         enabled route host ──────└──> Gateway
                                         │ VPS loopback port
                                         ▼
                                  managed sshd :2222
                                         ▲
                                         │ Agent initiates OpenSSH -R
Internal service <──────── Agent <───────┘
```

The easy install runs only `portloom-server` and `portloom-sshd`. Server retains an internal administration listener (default `127.0.0.1:8080`) and Gateway (default `127.0.0.1:8081`) while binding public ports 80/443 itself. Caddy, Nginx, or NPM is not required.

## Control and tunnel paths

Server stores Agents, token verifiers, SSH public keys, routes, allocated ports, and observations in SQLite. The WebUI uses the admin API; Agents enroll, sync, and heartbeat over HTTPS. One Server writer owns the database.

Each Agent keeps one OpenSSH ControlMaster and applies route changes with `ssh -O forward/cancel -R`. Managed sshd permits loopback remote forwarding only.

## Public requests and certificates

The native edge dispatches `TM_PUBLIC_HOST` to the control handler and other hostnames to the Gateway. Public management requests have 30-second read/write deadlines and a 1 MiB body limit to prevent slow clients from holding connections; these limits apply only to the management host, so Gateway application routes can still stream for longer. The Gateway selects only enabled, converged HTTP routes, then proxies through a VPS loopback port and the SSH tunnel to the Agent's local target.

Server uses autocert with ACME HTTP-01 on port 80 and persists certificates at `/data/certs`. Its HostPolicy authorizes only `TM_PUBLIC_HOST` and currently enabled HTTP route hostnames. Unknown names cannot trigger issuance, and port 80 redirects only names PortLoom owns. The container needs `NET_BIND_SERVICE` to bind 80/443.

## Legacy/advanced ingress

Existing Caddy, Nginx, or NPM can replace the native edge: proxy the management hostname to 8080 and application hostnames to 8081 with Host preserved. The optional `/api/v1/tls/allow` and `TM_TLS_ASK_*` remain only for external-Caddy on-demand TLS compatibility, not the default certificate path.

Running Agents preserve existing forwards during a brief Server outage and rebuild failed SSH masters. Host-key changes fail closed. Server rebuilds Agent authorization from SQLite at startup, and failed forward cancellation never reports false convergence.
