# Core concepts

## Server and Gateway

One Server process exposes an administration listener (default `127.0.0.1:8080`) and an HTTP Host gateway (default `127.0.0.1:8081`). The Gateway resolves an enabled route and proxies to its allocated SSH loopback port.

## Agent and Client

An Agent runs on the NAS, enrolls once, stores credentials in `/data/agent.json`, polls desired state, probes local services, reconciles OpenSSH `-R` forwards, and reports observations. Every enrolled Agent becomes an independent Client.

::: warning Current limitation
`tunnel_group` is persisted as metadata, but the current Agent uses one OpenSSH ControlMaster. Run separate Agent containers/Clients when Web and high-throughput media traffic must use independent SSH master connections.
:::

## Route

An HTTP route includes its domain, local target, enabled flag, and an automatically allocated VPS loopback port. TCP routes are currently control-plane metadata; the built-in Gateway only proxies HTTP by Host.

## Desired and observed state

An API write only changes desired state. Configuration is converged after the Agent reports the same revision. Local reachability, SSH connectivity, and revision convergence are reported separately.
