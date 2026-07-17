# What is PortLoom?

PortLoom is a self-hosted tunnel proxy. It publishes web services from a home NAS or private network through a Docker host with a public address.

## The two hosts

| Location | Install | Responsibility |
| --- | --- | --- |
| Public VPS or cloud host | PortLoom Server | WebUI, management API, HTTPS ingress, and tunnel entry |
| NAS or internal server | PortLoom Agent | Connects outbound to Server and forwards traffic to local services |

The Agent only needs outbound HTTPS and SSH access to Server. You do not open an inbound port on the NAS router.

## Request path

```text
Browser
  │ HTTPS
  ▼
Public Docker host
  Caddy → PortLoom Gateway
              │
              │ established encrypted reverse tunnel
              ▼
Internal Docker host
  PortLoom Agent → Jellyfin / blog / admin page
```

The Agent initiates the tunnel to Server. Requests travel back through that established tunnel to the internal service.

## Daily use

After installation, routine work happens in the WebUI:

1. Add an Agent and copy its generated install command.
2. Paste the command on the NAS.
3. Add a route with its local address, port, and public hostname.
4. Check local reachability and tunnel health.

## Current scope

This release fully manages hostname-based HTTP/HTTPS routes. TCP fields are compatibility metadata only: the built-in ingress and WebUI do not create public TCP listeners or report those records as published or healthy. Server uses one SQLite database and is not an active-active cluster.

Existing Caddy, Nginx, or Nginx Proxy Manager installations can remain in place. See [Reverse proxy integration](/en/install/reverse-proxy). They are optional integrations, not prerequisites.
