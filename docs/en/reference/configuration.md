# Configuration

The easy installers generate these values. Container paths must be absolute in hand-written Compose files.

## Server

| Variable | Default | Purpose |
| --- | --- | --- |
| `TM_ADMIN_TOKEN` | none | required, at least 16 characters |
| `TM_LISTEN_ADDR` | `127.0.0.1:8080` | internal/legacy upstream for WebUI and admin API |
| `TM_GATEWAY_ADDR` | `127.0.0.1:8081` | internal/legacy upstream for the HTTP Host Gateway |
| `TM_EDGE_HTTP_ADDR` | empty | native public HTTP/ACME listener; easy install uses `:80` |
| `TM_EDGE_HTTPS_ADDR` | empty | native public HTTPS listener; easy install uses `:443` |
| `TM_TLS_CACHE_DIR` | `/data/certs` | absolute persistent autocert cache path |
| `TM_ACME_EMAIL` | empty | optional ACME account contact email |
| `TM_PUBLIC_HOST` | empty | management hostname; required by native edge |
| `TM_DATABASE_PATH` | `/data/portloom.db` | absolute SQLite path |
| `TM_WEB_DIR` | `/app/web` | WebUI assets |
| `TM_PORT_RANGE_START` / `TM_PORT_RANGE_END` | `20000` / `29999` | SSH loopback pool |
| `TM_ENROLLMENT_TTL` | `1h` | default token lifetime, maximum 30 days |
| `TM_AUTHORIZED_KEYS_PATH` | empty | managed authorized_keys path |
| `TM_SSH_HOST_PUBLIC_KEY_PATH` | empty | managed sshd Ed25519 host public key |
| `TM_MANAGED_SSH_PORT` | `2222` | SSH port shown in generated Agent commands |
| `TM_MANAGED_SSH_ISOLATED` | `false` | isolate Agent forwarding with managed SSH paths |
| `TM_TLS_ASK_TOKEN` | empty | optional external-Caddy on-demand TLS compatibility token |
| `TM_TLS_ASK_ADDR` | `127.0.0.1:8082` | loopback listener for the optional `ask` endpoint |

`TM_EDGE_HTTP_ADDR` and `TM_EDGE_HTTPS_ADDR` must be configured together. Native mode also requires `TM_PUBLIC_HOST` and an absolute `TM_TLS_CACHE_DIR`. `TM_PUBLIC_HOST` must be a multi-label DNS name whose final label contains a letter; IP literals, abbreviated IP forms, integer forms, and `localhost` are rejected. A non-root container binding 80/443 needs `NET_BIND_SERVICE`.

The native edge uses autocert HTTP-01 and authorizes only `TM_PUBLIC_HOST` and currently enabled HTTP route hostnames. `TM_TLS_ASK_*` does not participate in native issuance; it exists only for external-Caddy compatibility. Setting the token requires `TM_PUBLIC_HOST`, and the listen address must use a loopback IP.

The two managed SSH paths must be set together. `TM_MANAGED_SSH_ISOLATED=true` also requires both paths.

## Managed sshd

`PORTLOOM_SSH_PORT` defaults to `2222`. Persist writable `/hostkeys`; mount Server's authorization volume read-only at `/auth`.

## Agent

| Variable | Default | Purpose |
| --- | --- | --- |
| `TM_SERVER_URL` | none | required; public deployments require HTTPS |
| `TM_CLIENT_NAME` / `TM_ENROLLMENT_TOKEN` | none | first-enrollment name and one-time token |
| `TM_AGENT_STATE_PATH` | `/data/agent.json` | persistent Agent identity |
| `TM_POLL_INTERVAL` | `30s` | sync and heartbeat interval |
| `TM_HEALTH_TIMEOUT` / `TM_REQUEST_TIMEOUT` | `3s` / `10s` | local probe/API timeout |
| `TM_SSH_HOST` / `TM_SSH_PORT` / `TM_SSH_USER` | none / `22` / none | easy install uses public host, 2222, and `tunnel` |
| `TM_SSH_IDENTITY_FILE` | none | Agent private key |
| `TM_SSH_PUBLIC_KEY_FILE` | `<identity>.pub` | Agent public key uploaded to Server |
| `TM_SSH_KNOWN_HOSTS_FILE` | none | pinned Server host key file |
| `TM_SSH_CONTROL_PATH` | `/tmp/portloom-%C.sock` | ControlMaster socket |
| `TM_ALLOW_INSECURE_HTTP` | `false` | loopback development HTTP only |
