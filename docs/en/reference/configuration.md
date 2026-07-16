# Configuration reference

## Server

| Variable | Default | Purpose |
| --- | --- | --- |
| `TM_ADMIN_TOKEN` | none | Required, at least 16 characters |
| `TM_LISTEN_ADDR` | `127.0.0.1:8080` | Console and API listener |
| `TM_GATEWAY_ADDR` | `127.0.0.1:8081` | HTTP Host gateway |
| `TM_DATABASE_PATH` | `/data/portloom.db` | Absolute SQLite path |
| `TM_WEB_DIR` | `/app/web` | Administration Web assets |
| `TM_PORT_RANGE_START/END` | built-in | SSH loopback allocation range |
| `TM_ENROLLMENT_TTL` | `1h` | Default token lifetime, max 30 days |

## Agent

| Variable | Default | Purpose |
| --- | --- | --- |
| `TM_SERVER_URL` | none | Required; HTTPS by default |
| `TM_CLIENT_NAME` | none | First-enrollment name |
| `TM_ENROLLMENT_TOKEN` | none | First enrollment only |
| `TM_AGENT_STATE_PATH` | `/data/agent.json` | Persistent identity |
| `TM_POLL_INTERVAL` | `30s` | Poll/heartbeat interval |
| `TM_HEARTBEAT_INTERVAL` | `30s` | compatibility alias |
| `TM_HEALTH_TIMEOUT` | `3s` | local probe timeout |
| `TM_REQUEST_TIMEOUT` | `10s` | control-plane request timeout |
| `TM_SSH_HOST/PORT/USER` | port `22` | restricted SSH endpoint |
| `TM_SSH_IDENTITY_FILE` | none | private key in container |
| `TM_SSH_KNOWN_HOSTS_FILE` | none | pinned host keys |
| `TM_SSH_CONTROL_PATH` | `/tmp/portloom-%C.sock` | ControlMaster socket |
| `TM_SSH_CONNECT_TIMEOUT` | `10` | SSH timeout in seconds |
| `TM_ALLOW_INSECURE_HTTP` | `false` | loopback development only |
