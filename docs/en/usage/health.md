# Health model

PortLoom intentionally avoids collapsing readiness into one green dot.

## Local

The Agent can connect to `local_host:local_port` within the configured timeout. A failure points to the target service, address, port, or NAS firewall.

## Tunnel

The OpenSSH ControlMaster is valid and the remote `-R` forward exists. Typical failures include key permissions, host-key mismatch, SSH account policy, and remote-port conflicts.

## Public / revision

Desired and observed revisions match and the route is enabled. This proves Agent convergence, not external DNS or TLS.

Troubleshoot in order: local NAS request → Agent Local state → SSH/Tunnel → Gateway with Host → NPM/TLS/DNS → public request.
