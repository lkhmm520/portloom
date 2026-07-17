# Configuration

The easy installers generate these values. Container paths must be absolute in hand-written Compose files.

## Server

| Variable | Default | Purpose |
| --- | --- | --- |
| `TM_ADMIN_TOKEN` | none | required, at least 16 characters |
| `TM_LISTEN_ADDR` | `127.0.0.1:8080` | WebUI and admin API |
| `TM_GATEWAY_ADDR` | `127.0.0.1:8081` | HTTP Host Gateway |
| `TM_DATABASE_PATH` | `/data/portloom.db` | absolute SQLite path |
| `TM_WEB_DIR` | `/app/web` | WebUI assets |
| `TM_PORT_RANGE_START` | `20000` | SSH loopback pool start |
| `TM_PORT_RANGE_END` | `29999` | SSH loopback pool end |
| `TM_ENROLLMENT_TTL` | `1h` | default token lifetime, maximum 30 days |
| `TM_AUTHORIZED_KEYS_PATH` | empty | managed authorized_keys path |
| `TM_SSH_HOST_PUBLIC_KEY_PATH` | empty | managed sshd Ed25519 host public key |
| `TM_MANAGED_SSH_PORT` | `2222` | SSH port shown in generated Agent commands |
| `TM_PUBLIC_HOST` | empty | management hostname for easy HTTPS |
| `TM_TLS_ASK_TOKEN` | empty | Caddy on-demand TLS authorization token |

The two managed SSH paths must be set together. `TM_PUBLIC_HOST` and `TM_TLS_ASK_TOKEN` must also be set together.

## Managed sshd

`PORTLOOM_SSH_PORT` defaults to `2222`. Persist writable `/hostkeys`; mount Server's authorization volume read-only at `/auth`.

## Agent

| Variable | Default | Purpose |
| --- | --- | --- |
| `TM_SERVER_URL` | none | required; public deployments require HTTPS |
| `TM_CLIENT_NAME` | none | first-enrollment name |
| `TM_ENROLLMENT_TOKEN` | none | first enrollment only |
| `TM_AGENT_STATE_PATH` | `/data/agent.json` | persistent Agent identity |
| `TM_POLL_INTERVAL` | `30s` | sync and heartbeat interval |
| `TM_HEALTH_TIMEOUT` | `3s` | local probe timeout |
| `TM_REQUEST_TIMEOUT` | `10s` | API timeout |
| `TM_SSH_HOST` | none | managed sshd host |
| `TM_SSH_PORT` | `22` | easy install uses 2222 |
| `TM_SSH_USER` | none | easy install uses `tunnel` |
| `TM_SSH_IDENTITY_FILE` | none | Agent private key |
| `TM_SSH_PUBLIC_KEY_FILE` | `<identity>.pub` | Agent public key uploaded to Server |
| `TM_SSH_KNOWN_HOSTS_FILE` | none | pinned Server host key file |
| `TM_SSH_CONTROL_PATH` | `/tmp/portloom-%C.sock` | ControlMaster socket |
| `TM_SSH_CONNECT_TIMEOUT` | `10` | SSH timeout in seconds |
| `TM_ALLOW_INSECURE_HTTP` | `false` | loopback development HTTP only |
