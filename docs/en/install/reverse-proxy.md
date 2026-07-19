# Reverse proxy integration

The default easy install lets PortLoom own 80/443. This page is only for advanced deployments that must retain existing Caddy, Nginx, or Nginx Proxy Manager.

## Disable native web edge

Leave `TM_EDGE_HTTP_ADDR`/`TM_EDGE_HTTPS_ADDR` unset and expose two protected upstreams:

| Upstream | Purpose |
| --- | --- |
| `127.0.0.1:8080` | management hostname, WebUI, API, `/healthz` |
| `127.0.0.1:8081` | legacy Web Gateway for application hostnames |

First define the WebSocket connection variable in Nginx's `http {}` context, not inside a `location`:

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}
```

Then add this inside the relevant `server {}`:

```nginx
location / {
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_pass http://127.0.0.1:8081;
}
```

A bridge-network proxy cannot reach host loopback through its own `127.0.0.1`; use host networking or a firewall-protected private bind. Never expose 8080/8081 directly.

## v0.4 compatibility boundaries

8081 is a **scheme-agnostic** legacy entry. External ingress owns HTTP/HTTPS, certificates, and redirects. Do not create both HTTP and HTTPS rows for the same Host/path because the legacy Gateway cannot distinguish them reliably. Path-prefix matching still works.

With native edge disabled:

- `/system.web_port_edge=false`, and the API rejects custom web `public_port`;
- management-host sub-path sharing and extra web listeners must be configured explicitly in the outer proxy;
- TCP/UDP stream edge remains independent unless `TM_TCP_EDGE_BIND_HOST=off`.

## External Caddy `ask`

Set `TM_PUBLIC_HOST` and `TM_TLS_ASK_TOKEN`, then let Caddy query only `127.0.0.1:8082/api/v1/tls/allow`. It authorizes the management hostname and enabled **HTTPS** route hostnames, not HTTP routes. `TM_TLS_ASK_ADDR` must be loopback; never publish the endpoint or token.

## Verify

```bash
curl -i -H 'Host: app.example.com' http://127.0.0.1:8081/
curl -I https://app.example.com/
```

404 means no enabled, ready route matched. 502 means a route matched but Agent tunnel or local service is unavailable. For media applications, also verify WebSocket, Range, upload limits, and long-lived timeouts.
