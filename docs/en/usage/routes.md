# Route management

| Field | Meaning |
| --- | --- |
| Name | Human-readable route name |
| Client | Agent that carries the route |
| Domain | Unique public hostname for HTTP |
| Local host/port | Target reachable from the Agent host |
| Tunnel group | Metadata today; use separate Agents for isolation |
| Enabled | Included in desired state and Gateway lookup |

Agents use host networking, so `127.0.0.1` refers to the NAS host. A mapped host port or LAN address can target another container.

Create and observe a route before switching an existing ingress, if any. Before deleting a route, restore its previous upstream. The easy installer's Caddy needs no per-route edits. Validate Range, WebSocket, large-file, and long-lived request behavior when the application needs them.

::: warning TCP compatibility metadata
The WebUI creates HTTP routes only. TCP records left by the API or an older version remain visible as control-plane metadata: the built-in Gateway creates no public TCP listener, Public shows `metadata only`, and the record is never counted as healthy. Such records can be deleted but not created or edited in the WebUI.
:::
