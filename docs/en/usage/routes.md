# Route management

## Four protocols

| Protocol | Public behavior |
| --- | --- |
| HTTPS | Route by domain, path, and port with automatic ACME certificates. HTTP receives a 308 redirect only when no plain-HTTP route matches the same Host |
| HTTP | Publish in plaintext on the HTTP edge without certificate issuance or forced redirect |
| TCP | Listen on an explicit public VPS TCP port and forward bytes to the Agent target |
| UDP | Listen on an explicit public VPS UDP port and relay datagrams as length-prefixed frames inside the SSH tunnel |

When upgrading an early database, v0.4 migrates legacy `http` rows to `https` to preserve the old automatic-TLS behavior. A newly created `http` route now means real plaintext HTTP.

## Create in the WebUI

1. Open **Routes** and click **Add route**; a new route defaults to **HTTPS**.
2. Select Client and fill Name, protocol-specific public fields, Local host/port, Tunnel group, and Enabled.
3. Save and wait for Local, Tunnel, and Public to converge. The WebUI refreshes once per second; the heartbeat publication window is about 90 seconds.
4. Run a real end-to-end protocol test. `published` does not itself verify public DNS, firewalls, or a UDP target response.

When editing an existing route, the WebUI locks Client and cannot directly move the route to another Agent. For a Client migration, record the configuration and plan a delete/recreate cutover window to avoid endpoint uniqueness conflicts.

## Web routes

| Field | Meaning |
| --- | --- |
| Domain | Public DNS hostname; IPs, ports, single labels, and numeric final labels are rejected |
| Path prefix | Optional; `/` is normalized to empty. Must start with `/` and contain no empty, `.`, or `..` segment |
| Strip path | Remove the prefix before proxying; requires a non-empty prefix |
| Public port | empty uses the protocol's primary edge; explicit HTTP 80 / HTTPS 443 also normalize to the primary edge. Other allowed ports in 1–65535 create a dynamic extra TCP listener |
| Local host/port | Target reachable by Agent. With host networking, `127.0.0.1` is the NAS host |
| Tunnel group | Metadata today; use a separate Agent for connection isolation |

A web endpoint is unique by `(protocol, domain, public port, path prefix)`, and longest path prefix wins. Multiple routes of the same scheme may share one extra port. HTTP and HTTPS cannot share the same extra port; API/WebUI rejects the write with 409 conflict. Public `conflict` is a defensive state for a legacy or otherwise inconsistent database.

For example, `/media` matches only `/media` and `/media/...`, never `/mediabox`. With Strip path enabled, `/media/tv` becomes `/tv`, and an exact `/media` request becomes `/`. Prefix `/` is normalized to empty and therefore matches the whole hostname.

The management hostname can host a non-empty path prefix only on the primary HTTPS edge. It cannot use HTTP, a custom port, the root path, or a prefix under `/api`, `/assets`, or `/healthz`.

::: tip Applications on a sub-path
`Strip path` changes only the request path sent upstream. It does not rewrite absolute response URLs, Cookie Path, or frontend asset URLs. Prefer a dedicated hostname when the application does not support sub-path deployment.
:::

## TCP / UDP routes

TCP/UDP routes have no domain, path, or Strip path and require Public port. One stream public port can belong to only one TCP/UDP route and cannot use control, Gateway, SSH, HTTP/HTTPS edge, or tunnel-pool (`TM_PORT_RANGE_START..END`) ports.

The stream edge binds `0.0.0.0` by default. Set `TM_TCP_EDGE_BIND_HOST=off` to disable it, or a literal IP such as `127.0.0.1` or `::` to restrict the bind. The WebUI disables new TCP/UDP routes when the edge is off.

UDP sessions are keyed by public source address and expire after 60 idle seconds. The internal two-byte frame length field tops out at 65,535, but that is **not** a promised public UDP payload: ordinary IPv4/IPv6 UDP limits are usually 65,507/65,527 bytes, further constrained by path MTU and socket behavior. This is intended for small and medium datagrams such as DNS, game control traffic, or WireGuard handshakes, not native-UDP throughput or head-of-line-loss resistance. The stream manager currently has fixed limits of 1,024 active sessions/connections globally and 128 per route, with no public configuration knobs.

## Status, DNS, and firewall

- `waiting_agent`: revision/Tunnel/heartbeat has not converged (heartbeat freshness is 90 seconds);
- `pending`: a dynamic listener is being reconciled;
- `published`: PortLoom has built the public processing path;
- `conflict` / `bind_error`: routes/schemes compete for the port or the OS bind failed;
- `disabled`: the route is not enabled.

`published` does not validate external DNS or firewall policy. Configure A/AAAA for web routes; allow TCP for custom web/TCP ports and UDP for UDP routes. Certificates are authorized only for enabled HTTPS hostnames. Disabling a route does not remove cached files under `/data/certs`.

## Traffic and resources

Dashboard's chart is the sum of inbound and outbound bytes in the latest 60 one-minute buckets. Requests, Bytes In, and Bytes Out are cumulative since Server start. Each TCP connection and UDP session counts as one request/event. Resource primary values are PortLoom process CPU and RSS: one saturated core is about 100% CPU, while the memory percentage in parentheses is host/container-namespace total memory use—not RSS as a percentage.

`GET /api/v1/metrics` additionally returns per-route totals. Metrics are in memory and reset on Server restart. Currently, deleting a route does not immediately remove that route ID's accumulated counters from the metrics API. Agent resources arrive with sync/heartbeats, and the current UI does not mark resource samples stale.
