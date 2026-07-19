# Architecture

```text
                         Public Docker host
 Web ──HTTP/HTTPS──> primary/extra web edge ── Gateway ──┐
 TCP/UDP ─────────> stream edge ─────────────────────────┤
                                                          │ allocated loopback port
                                                          ▼
                                                   managed sshd :2222
                                                          ▲
                                                          │ outbound OpenSSH -R
 Internal HTTP/TCP/UDP service <──────── Agent <───────────┘
```

The easy install runs only `portloom-server` and `portloom-sshd`. One Server process owns the control plane, web Gateway, primary HTTP/HTTPS edges, dynamic extra web listeners, TCP/UDP stream edge, SQLite, and in-memory metrics.

## Control plane

The administration listener defaults to `127.0.0.1:8080` and serves the WebUI, `/api/v1/*`, and `/healthz`. SQLite stores Agents, token verifiers, SSH keys, routes, allocated loopback ports, revisions, and latest observations. Admin and Agent endpoints use separate Bearer credentials.

Server applies forward SQLite schema migrations at startup. v0.4 adds path fields/indexes and migrates early legacy `http` rows to `https` to preserve their automatic-TLS behavior. There is one Server writer.

## Web data plane

- Primary HTTP/HTTPS edges default to 80/443 but may bind other local ports. Routes without `public_port` follow these primary listeners rather than hard-coded 80/443.
- The extra-web manager reconciles routes with `public_port` every second. One port runs one scheme, while that scheme may carry multiple Hosts/paths.
- Gateway matches scheme/port/Host and then chooses the ready route with the longest path prefix. `strip_path` changes only the upstream request path; the original Host is preserved, while `X-Forwarded-*` is rebuilt from the trusted peer address, Host, and ingress scheme rather than trusting client-supplied values.
- The native router protects control paths on the management hostname while allowing safe HTTPS sub-path routes.

HTTPS uses autocert and ACME HTTP-01 with cache under `/data/certs`. HostPolicy authorizes only the management hostname and enabled HTTPS route hostnames. Public port 80 must reach the primary HTTP edge.

## SSH and stream data plane

Each Agent owns one OpenSSH ControlMaster. Every route receives a VPS loopback port, and Agent adds/removes `ssh -O forward/cancel -R` dynamically. Managed sshd permits loopback remote forwards only; isolated mode assigns distinct loopback addresses to Agents.

TCP edge maps a public connection to that loopback port. UDP edge keeps a session by public source: the VPS encodes datagrams as length-prefixed frames, sends them through loopback TCP/SSH to Agent's local UDP relay, and the relay talks to the real UDP service. Sessions expire after 60 idle seconds.

## State and metrics

Agent heartbeats carry global revision, per-route Local/Tunnel observations, and process resources. Publication requires enabled state, Tunnel up, observed revision **equal** to the current desired revision, and heartbeat age within 90 seconds. A lower revision has not converged; a higher one is rejected by the API. Dynamic listeners additionally report pending/conflict/bind_error/published.

Server keeps in-memory request/session and bidirectional-byte counters plus 60 minute buckets. CPU is process usage between samples, where 100% is one core; RSS is resident process memory. Metrics reset on Server restart.

## External-ingress compatibility

With native edge disabled, 8080 is the administration upstream and 8081 is the legacy scheme-agnostic web Gateway. External ingress owns TLS, redirects, and public ports; custom web `public_port` is unavailable. TCP/UDP stream edge is independent and may remain enabled.
