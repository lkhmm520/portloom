# Core concepts

## Server and Gateway

One Server process always exposes two internal listeners: an administration listener (default `127.0.0.1:8080`) and an HTTP Host Gateway (default `127.0.0.1:8081`). The easy install also enables Server’s native public edge on ports 80/443; it terminates HTTPS and dispatches the management hostname to control and enabled HTTP route hostnames to the Gateway. The Gateway proxies to each route’s allocated SSH loopback port.

## Agent and Client

An Agent runs on the NAS, enrolls once, stores credentials in `/data/agent.json`, polls desired state, probes local services, reconciles OpenSSH `-R` forwards, and reports observations. Every enrolled Agent becomes an independent Client.

::: warning Current limitation
`tunnel_group` is persisted as metadata, but the current Agent uses one OpenSSH ControlMaster. Run separate Agent containers/Clients when Web and high-throughput media traffic must use independent SSH master connections.
:::

## Route

An HTTP route includes its domain, local target, enabled flag, and an automatically allocated VPS loopback port. The WebUI creates HTTP routes only. Existing TCP records are compatibility metadata and do not represent public listeners; the built-in Gateway proxies HTTP by Host.

## Desired and observed state

An API write only changes desired state. Configuration is converged after the Agent reports the same revision. Local reachability, SSH connectivity, and revision convergence are reported separately.
