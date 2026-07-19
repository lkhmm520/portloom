# Core concepts

## Server, Gateway, and public edges

One Server process contains the administration listener (default `127.0.0.1:8080`) and the legacy Gateway (default `127.0.0.1:8081`). The easy install also enables native HTTP/HTTPS edges. Their primary ports default to 80/443 and may be moved to other local ports by the installer.

Gateway selects a ready web route by incoming **scheme, port, Host, and path**. The longest matching path prefix wins, and `strip_path` can remove that prefix before proxying. Server dynamically opens extra listeners for non-default web public ports.

The stream edge dynamically publishes TCP/UDP routes on explicit public ports. TCP forwards bytes. UDP keeps sessions by public source address and carries framed datagrams over a TCP connection inside the tunnel.

## Agent and Client

After enrollment, Agent persists its long-lived identity in `/data/agent.json`. It pulls desired state, probes local targets, reconciles OpenSSH `-R` forwards, sends heartbeats, and reports process resources. Each enrolled Agent is a Client in the API.

::: warning Connection isolation
`tunnel_group` is persisted, but one Agent currently owns one OpenSSH ControlMaster. Run two Agents/Clients with separate state directories when web and high-volume media traffic need separate SSH master connections.
:::

## Route

Every route has a name, Client, local target, enabled state, desired/observed revision, and an allocated VPS loopback port.

- Web route: `http` or `https`, requires a domain, and may specify a path prefix, prefix stripping, and custom public port.
- Stream route: `tcp` or `udp`, has no domain/path and requires a public port.
- A web endpoint is unique by `(protocol, domain, public port, path prefix)`. A TCP/UDP public port can belong to only one stream route.

## Desired, observed, and published state

An API write changes desired state only. A route becomes publication-ready after Agent reports an observed revision **equal** to the current desired revision, Tunnel is `up`, and its heartbeat is fresh (90-second window). A lower revision has not converged; a higher revision is rejected by the API. Local, Tunnel, and Public are separate; `published` does not prove external DNS, certificates, or firewall policy.

## Metrics

Server keeps in-memory request/session counts, byte totals, a rolling 60-minute series, and CPU/RSS samples for itself and Agents. Deleting a route does not currently clear that route ID's accumulated counters immediately. Restarting Server resets all metrics.
