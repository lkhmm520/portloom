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

Create and observe a route before switching NPM. Before deleting a route, restore NPM to the previous upstream. Validate Range, WebSocket, large-file, and long-lived request behavior when the application needs them.

::: warning TCP routes
The current release stores TCP route and public-port metadata, but the built-in Gateway only proxies HTTP by Host. Do not treat TCP metadata as an automatically published listener.
:::
