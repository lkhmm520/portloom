# What is PortLoom?

PortLoom is a self-hosted tunnel proxy. It publishes web services from a home NAS or private network through a Docker host with a public address.

| Location | Install | Responsibility |
| --- | --- | --- |
| Public VPS or cloud host | PortLoom Server + managed sshd | WebUI, management API, native HTTPS edge, and tunnel entry |
| NAS or internal server | PortLoom Agent | Connects outbound to Server and forwards traffic to local services |

The Agent needs only outbound HTTPS and SSH access to Server. You do not open an inbound port on the NAS router.

```text
Browser ──HTTPS──> PortLoom Server native edge/Gateway
                              │ established encrypted reverse tunnel
                              ▼
                    PortLoom Agent → internal service
```

The easy install needs no Caddy: Server binds 80/443, manages certificates with autocert HTTP-01, and authorizes only the management hostname and enabled HTTP route hostnames.

After installation, add an Agent in the WebUI, run its generated command, and create HTTP routes with a local address, port, and public hostname.

This release fully manages hostname-based HTTP/HTTPS routes. TCP fields are compatibility metadata only; the built-in edge and WebUI create no public TCP listeners. Server uses one SQLite database and is not an active-active cluster.

Existing Caddy, Nginx, or Nginx Proxy Manager can remain as an advanced compatibility ingress using 8080/8081. See [Reverse proxy integration](/en/install/reverse-proxy).
