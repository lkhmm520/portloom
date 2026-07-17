# Reverse proxy integration

The easy installer no longer includes Caddy: PortLoom Server owns public ports 80/443 and certificates by default. This page covers legacy/advanced integration with an existing Caddy, Nginx, or Nginx Proxy Manager instance.

## Disable the native edge and configure upstreams

When the external ingress owns 80/443, leave `TM_EDGE_HTTP_ADDR` and `TM_EDGE_HTTPS_ADDR` unset. PortLoom exposes two legacy HTTP upstreams:

| Upstream | Purpose |
| --- | --- |
| `127.0.0.1:8080` | management hostname, WebUI, and API |
| `127.0.0.1:8081` | shared Gateway for all HTTP application hostnames |

Send only the management hostname to 8080. Send application hostnames to 8081 with the original Host header. The external ingress terminates TLS.

```nginx
location / {
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_pass http://127.0.0.1:8081;
}
```

Set WebSocket headers, upload limits, and timeouts for each application, and test Range requests for media services. A bridge-network proxy cannot reach host loopback through its own `127.0.0.1`; use host networking or a firewall-protected private bind. Never expose management port 8080 directly to the internet.

## External Caddy `ask` compatibility

An external Caddy deployment that retains on-demand TLS can set `TM_PUBLIC_HOST` and `TM_TLS_ASK_TOKEN`, then call `/api/v1/tls/allow` on the default loopback address `127.0.0.1:8082`. It authorizes only the management hostname and enabled HTTP route hostnames. `TM_TLS_ASK_ADDR` must use a loopback IP; do not expose the endpoint or token.

This mode is optional compatibility. The native HTTPS edge uses autocert HostPolicy directly and needs neither `TM_TLS_ASK_*` nor `/api/v1/tls/allow`.

A Gateway 404 means no enabled HTTP route matches Host. A 502 means a route matched but the Agent tunnel or local service is unavailable.
