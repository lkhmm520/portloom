# Health model

PortLoom intentionally avoids collapsing readiness into one green dot.

## Local

The Agent can connect to `local_host:local_port` within the configured timeout. A failure points to the target service, address, port, or NAS firewall.

## Tunnel

The OpenSSH ControlMaster is valid and the remote `-R` forward exists. Typical failures include key permissions, host-key mismatch, SSH account policy, and remote-port conflicts.

## Public / revision

For an HTTP route, Public is published only when desired and observed revisions match, the route is enabled, and Tunnel is up. This proves Agent and built-in Gateway convergence, not external DNS or TLS. TCP compatibility records always show `metadata only` and are never counted as published or healthy.

Troubleshoot in order: local NAS request → Agent Local state → SSH/Tunnel → Gateway with Host → public ingress/TLS/DNS → public request.
