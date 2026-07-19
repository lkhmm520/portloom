# Route management

## Protocols

| Protocol | Behavior |
| --- | --- |
| HTTPS | Published by domain (optional path prefix) with automatic ACME certificates; HTTP requests receive a 308 redirect |
| HTTP | Published in plain text on the HTTP edge port by domain (optional path prefix); no certificate, no redirect |
| TCP | Listens on the selected public VPS port and forwards bytes to the Agent's local service |
| UDP | Listens on the selected public UDP port; datagrams are carried across the tunnel through a length-prefixed relay |

One domain can host multiple routes: different path prefixes (longest prefix wins), different public ports (multiple web routes of the same scheme may share one extra port), and HTTP alongside HTTPS. The management domain also accepts path-prefix routes (HTTPS on the default port only, and never `/api`, `/assets`, or `/healthz`).

## Web route fields

| Field | Meaning |
| --- | --- |
| Name | Human-readable route name |
| Client | Agent that carries the route |
| Domain | Public DNS hostname; IPs, ports, single labels, and numeric final labels are rejected |
| Path prefix | Optional prefix starting with `/`, such as `/jellyfin`; empty means the whole domain |
| Strip path | Remove the prefix before forwarding, for apps without sub-path support |
| Public port | Optional; empty means the default 80/443. Another port makes the Server open an extra listener |
| Local host/port | Target reachable from the Agent host |
| Tunnel group | Metadata today; use separate Agents for isolation |
| Enabled | Included in desired state, Gateway lookup, and native certificate authorization |

TCP/UDP routes have no domain or path and require `Public port`. Agents use host networking, so `127.0.0.1` refers to the NAS host. A mapped host port or LAN address can target another container.

## Publishing and safe changes

Point the new hostname's A/AAAA record to the VPS, then enable the route. The route API and certificate authorization use the same strict public-DNS validation, so values that cannot receive a certificate—such as IPs, names with ports, or `localhost`—are rejected before they are saved. The native edge obtains ACME HTTP-01 certificates for **HTTPS** routes only; plain-HTTP routes never authorize certificates. Disabling the route removes future certificate authorization, while cached certificates remain under `/data/certs`.

Create and observe a route before switching an existing ingress, if any. Before deleting a route, restore its previous upstream. Validate Range, WebSocket, large-file, and long-lived request behavior when the application needs them.

## TCP / UDP publication

The stream edge is enabled by default and binds `0.0.0.0`; set `TM_TCP_EDGE_BIND_HOST=off` to disable it or another literal IP to restrict the bind. Port conflicts and bind failures surface in the WebUI Public layer (`conflict` / `bind_error`). UDP sessions are keyed by public source address and expire after 60 idle seconds; because the encapsulation rides the TCP tunnel, it suits small and medium datagrams such as DNS, game traffic, or WireGuard handshakes.

## Traffic and resource metrics

The dashboard shows the last 60 minutes of total traffic, request and byte counters, and CPU/memory usage of the Server and each Agent (reported with heartbeats). Metrics live in memory and reset when the Server restarts. The admin API is `GET /api/v1/metrics`.
