# Reverse proxy integration

PortLoom deliberately leaves TLS at NPM, Caddy, or Nginx.

## Administration host

Proxy an access-controlled hostname such as `portloom.example.com` to Server port `8080`, enable TLS, force HTTPS, and preferably add a VPN or reverse-proxy access list.

## Application hosts

All HTTP application hostnames can share port `8081`; the original `Host` must be preserved.

```nginx
location / {
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_pass http://127.0.0.1:8081;
}
```

If the reverse proxy is containerized, its `127.0.0.1` is not the host. Use host networking, a reviewed host-gateway address, or a private interface binding.

A Gateway 404 usually means no enabled HTTP route matches the Host. A 502 means a route matched but its SSH loopback listener is unavailable.
