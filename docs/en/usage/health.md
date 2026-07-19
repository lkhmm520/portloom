# Health model

PortLoom does not collapse everything into one green indicator. Read Local, Tunnel, Public, and Revision separately for every route.

## Local

For HTTP/HTTPS/TCP, Agent can connect to `local_host:local_port` within `TM_HEALTH_TIMEOUT`. Local down normally means a missing listener, wrong address/port, container networking, or NAS firewall. UDP targets cannot be probed reliably: Local up means only that Agent's UDP relay was created, so perform an end-to-end datagram test.

## Tunnel

The OpenSSH ControlMaster is valid and the allocated VPS loopback `-R` exists. UDP also depends on a TCP reverse forward for its framed datagram relay. Typical failures are private-key permissions, pinned host-key mismatch, managed-SSH policy, or loopback-port conflict.

## Revision and heartbeat

An API write advances desired revision. Agent must report an observed revision **equal** to the current desired revision, and its last heartbeat must be within 90 seconds, before publication is ready. A lower revision has not converged; a higher revision is rejected by the API. `waiting_agent` usually means revision, Tunnel, or heartbeat is not ready.

## Public

| Status | Meaning |
| --- | --- |
| `published` | Gateway or dynamic listener is ready; external DNS/firewall/TLS may still be wrong |
| `waiting_agent` | Agent has not converged, Tunnel is not up, or heartbeat is stale |
| `pending` | TCP/UDP or extra web listener is reconciling |
| `conflict` | incompatible routes compete for one port |
| `bind_error` | the OS could not bind the public port, commonly because it is occupied or permission is missing |
| `disabled` / `*_edge_disabled` | route or the corresponding edge is off |

## Troubleshooting order

1. Reach the local target directly on the NAS.
2. Inspect Agent install/runtime logs and Local.
3. Check heartbeat, revision, VPS loopback port, and Tunnel.
4. Test web routes with the correct scheme, port, Host, and path.
5. Test TCP/UDP from an external client with the real protocol.
6. Only then inspect DNS, ACME, cloud firewall, NAT, and ISP policy.
