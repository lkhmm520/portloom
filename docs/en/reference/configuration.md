# Configuration reference

The easy installers generate configuration. Prefer installer flags over hand-editing generated files. Container paths must be absolute in hand-written Compose files.

## Server

| Variable | Default | Meaning |
| --- | --- | --- |
| `TM_ADMIN_TOKEN` | none | required, at least 16 characters |
| `TM_LISTEN_ADDR` | `127.0.0.1:8080` | internal WebUI/admin API listener |
| `TM_GATEWAY_ADDR` | `127.0.0.1:8081` | legacy scheme-agnostic Gateway upstream |
| `TM_EDGE_HTTP_ADDR` | empty | primary native HTTP/ACME edge; easy install uses `:80` or `--http-port` |
| `TM_EDGE_HTTPS_ADDR` | empty | primary native HTTPS edge; easy install uses `:443` or `--https-port` |
| `TM_TCP_EDGE_BIND_HOST` | `0.0.0.0` | TCP/UDP stream-edge bind IP; a literal IP, or `off`/`disabled`/`none` to disable it. Extra web ports instead use the primary HTTP edge bind host |
| `TM_TLS_CACHE_DIR` | `/data/certs` | persistent absolute autocert cache |
| `TM_ACME_EMAIL` | empty | optional ACME account contact |
| `TM_PUBLIC_HOST` | empty | management hostname; required for native edge or TLS ask |
| `TM_DATABASE_PATH` | `/data/portloom.db` | SQLite absolute path |
| `TM_WEB_DIR` | `/app/web` | WebUI static directory |
| `TM_PORT_RANGE_START/END` | `20000` / `29999` | SSH loopback pool; public stream ports cannot overlap |
| `TM_ENROLLMENT_TTL` | `1h` | default token lifetime, maximum 30 days |
| `TM_AUTHORIZED_KEYS_PATH` | empty | managed `authorized_keys` absolute path |
| `TM_SSH_HOST_PUBLIC_KEY_PATH` | empty | managed sshd Ed25519 public-key path |
| `TM_MANAGED_SSH_PORT` | `2222` | SSH port returned by `/system` and Agent commands |
| `TM_MANAGED_SSH_ISOLATED` | `false` | isolate loopback bindings by Agent |
| `TM_TLS_ASK_TOKEN` | empty | external-Caddy on-demand TLS token |
| `TM_TLS_ASK_ADDR` | `127.0.0.1:8082` | dedicated loopback ask listener |

`TM_EDGE_HTTP_ADDR` and `TM_EDGE_HTTPS_ADDR` must be set together. Native edge also requires a valid `TM_PUBLIC_HOST` and absolute `TM_TLS_CACHE_DIR`. A non-root process needs `NET_BIND_SERVICE` for low ports. ACME HTTP-01 always requires **public port 80** to reach `TM_EDGE_HTTP_ADDR`, even when local `--http-port` differs.

The stream edge is enabled by default. It cannot use control/Gateway/TLS-ask, primary edge, managed SSH, or tunnel-pool ports. Extra web listeners are available only with native edge enabled and use the primary HTTP edge's bind host.

## Server installer

| Flag/environment | Meaning |
| --- | --- |
| `--domain` | required management hostname |
| `--web-port` / `--ssh-port` | internal administration / public managed-SSH port |
| `--http-port` / `--https-port` | local primary edges; public 80 must still forward to HTTP |
| `--version` | pinned image tag; use an exact version such as `0.4.1` in production |
| `--disable-tcp-edge` | write `PORTLOOM_TCP_EDGE_BIND_HOST=off` |
| `--enable-tcp-edge` | compatibility flag; v0.4 is enabled by default |
| `PORTLOOM_TCP_EDGE_BIND_HOST` | use with `--enable-tcp-edge` to override the bind IP before first install |
| `PORTLOOM_GATEWAY_PORT` | set the local legacy Gateway port; repeat the original value for a non-default upgrade |
| `PORTLOOM_HOME` | installation directory |

Stream-edge options reliably define first install. Once an existing `.env` supplies a non-empty value, the installer preserves it; do not treat rerun `--enable/--disable-tcp-edge` as a general toggle for installed systems.

## Agent

| Variable | Default | Meaning |
| --- | --- | --- |
| `TM_SERVER_URL` | none | required; public deployments require HTTPS, HTTP is loopback-only development |
| `TM_CLIENT_NAME` / `TM_ENROLLMENT_TOKEN` | none | first enrollment; installer removes the one-time token after success |
| `TM_CLIENT_ID` / `TM_AGENT_TOKEN` | none | post-enrollment long-lived identity, normally loaded from `/data/agent.json`; do not hand-edit or expose |
| `TM_AGENT_STATE_PATH` | `/data/agent.json` | persistent identity |
| `TM_POLL_INTERVAL` | `30s` | sync/heartbeat interval; legacy `TM_HEARTBEAT_INTERVAL` is accepted |
| `TM_HEALTH_TIMEOUT` / `TM_REQUEST_TIMEOUT` | `3s` / `10s` | local probe / API timeout |
| `TM_SSH_HOST` / `TM_SSH_PORT` / `TM_SSH_USER` | none / `22` / none | easy install uses public host, 2222, `tunnel` |
| `TM_SSH_IDENTITY_FILE` | none | private key; legacy `TM_SSH_PRIVATE_KEY_PATH` accepted |
| `TM_SSH_PUBLIC_KEY_FILE` | empty | key uploaded to Server; easy installer explicitly sets its generated `.pub` path |
| `TM_SSH_KNOWN_HOSTS_FILE` | none | pinned Server key; legacy `TM_SSH_KNOWN_HOSTS_PATH` accepted |
| `TM_SSH_CONTROL_PATH` | `/tmp/portloom-%C.sock` | ControlMaster socket |
| `TM_SSH_CONNECT_TIMEOUT` | `10` | OpenSSH connect timeout in seconds |
| `TM_MANAGED_SSH_READY_PATH` / `TM_MANAGED_SSH_READY_NONCE` | `/data/managed-ssh.ready` / empty | installer managed-SSH readiness handshake |
| `TM_MANAGED_SSH_ISOLATED` | `false` | Agent-side isolated-binding negotiation; requires the public-key file |
| `TM_ALLOW_INSECURE_HTTP` | `false` | only localhost, 127.0.0.0/8, or `::1` development URLs |
