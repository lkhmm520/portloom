# Reverse proxy integration

The easy installer already includes Caddy. Use this page when the public host already runs Caddy, Nginx, or Nginx Proxy Manager, or when another ingress must own HTTPS.

| Upstream | Purpose |
| --- | --- |
| `127.0.0.1:8080` | management hostname, WebUI, and API |
| `127.0.0.1:8081` | shared Gateway for all HTTP application hostnames |

Send the management hostname to 8080. Send application hostnames to 8081 and preserve the original Host header.

```nginx
location / {
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_pass http://127.0.0.1:8081;
}
```

Set WebSocket headers, upload limits, and timeouts for each application. Test Range requests for media services.

A bridge-network proxy cannot reach host loopback through its own `127.0.0.1`. Use host networking or a firewall-protected private bind. Do not expose management port 8080 directly to the internet.

A Gateway 404 means no enabled HTTP route matches Host. A 502 means a route matched but the Agent tunnel or local service is unavailable.
