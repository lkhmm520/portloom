# Route management

| Field | Meaning |
| --- | --- |
| Name | Human-readable route name |
| Client | Agent that carries the route |
| Domain | Unique public hostname for HTTP |
| Local host/port | Target reachable from the Agent host |
| Tunnel group | Metadata today; use separate Agents for isolation |
| Enabled | Included in desired state, Gateway lookup, and native certificate authorization |

Agents use host networking, so `127.0.0.1` refers to the NAS host. A mapped host port or LAN address can target another container.

## Publishing and safe changes

Point the new hostname's A/AAAA record to the VPS, then enable the route. The native edge automatically adds the hostname to HTTPS authorization and obtains a certificate with ACME HTTP-01; no per-route Caddy or proxy edit is needed. Disabling the route removes future certificate authorization, while cached certificates remain under `/data/certs`.

Create and observe a route before switching an existing ingress, if any. Before deleting a route, restore its previous upstream. Validate Range, WebSocket, large-file, and long-lived request behavior when the application needs them.

::: warning TCP compatibility metadata
The WebUI creates HTTP routes only. TCP records left by the API or an older version remain visible as control-plane metadata: neither the built-in Gateway nor native edge creates a public TCP listener, Public shows `metadata only`, and the record is never counted as healthy.
:::
