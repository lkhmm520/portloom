# What is PortLoom?

PortLoom is a reverse-SSH tunnel control plane for NAS, homelab, and small self-hosted deployments. Server and managed sshd run on a public host. Agent runs inside the private network and connects outbound, so the NAS router needs no inbound port.

| Location | Components | Responsibility |
| --- | --- | --- |
| Public VPS/cloud host | PortLoom Server + managed sshd | WebUI, API, native HTTP/HTTPS edge, TCP/UDP listeners, and SSH tunnel entry |
| NAS/internal host | PortLoom Agent | Pull desired routes, probe local services, maintain reverse forwards, and report state/resources |

```text
Web request ──HTTP/HTTPS──> native edge ──Gateway────┐
TCP/UDP client ───────────> Server stream edge ─────┤
                                                      │ VPS loopback port
                                                      ▼
                                               managed sshd :2222
                                                      ▲
                                                      │ outbound OpenSSH -R
Internal service <────────────────────── Agent <──────┘
```

## What v0.4 provides

- **HTTPS** routes by domain and optional path prefix with automatic ACME certificates. If no plain-HTTP route matches, HTTP for a host with HTTPS enabled receives a 308 redirect.
- **HTTP** routes in plaintext without certificate issuance or forced redirect.
- **TCP / UDP** routes on an explicit public VPS port. UDP datagrams use a length-prefixed relay inside the SSH tunnel.
- **Endpoint reuse** through path prefixes and custom public ports; safe HTTPS path routes may also share the management hostname.
- **Observability** for three-layer health, 60-minute traffic totals, and Server/Agent CPU and memory; the metrics API also exposes per-route counters.
- **Safe enrollment** through a one-time command with a pinned SSH host key; unused enrollment tokens can be revoked in the console.

The default install needs no Caddy, Nginx, or NPM. Server owns 80/443 and manages certificates with autocert HTTP-01. Existing ingress can still integrate through the legacy 8080/8081 upstreams.

## Current boundaries

- Server uses one SQLite database and is designed for a single writer, not active/active operation.
- Each Agent currently uses one OpenSSH ControlMaster. `tunnel_group` remains metadata; run multiple Agents/Clients for connection isolation.
- UDP is encapsulated over the TCP-based SSH tunnel and is intended for small and medium datagrams, not native-UDP throughput.
- Traffic and resource metrics are in memory and reset when Server restarts.
