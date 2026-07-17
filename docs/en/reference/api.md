# HTTP API

Admin endpoints use `Authorization: Bearer <admin-token>`. Agent endpoints use the Agent token returned after enrollment.

| Method | Path | Authentication | Purpose |
| --- | --- | --- | --- |
| GET | `/api/v1/system` | admin | managed SSH state, port, and Server host key |
| GET | `/api/v1/clients` | admin | list Agents |
| GET/POST | `/api/v1/enrollment-tokens` | admin | list/create one-time tokens |
| GET/POST | `/api/v1/routes` | admin | list/create routes |
| GET/PUT/DELETE | `/api/v1/routes/{id}` | admin | route CRUD |
| POST | `/api/v1/agent/enroll` | one-time token | first enrollment |
| PUT | `/api/v1/agent/ssh-key` | Agent | register or update the Agent Ed25519 key |
| GET | `/api/v1/agent/sync` | Agent | fetch desired routes |
| POST | `/api/v1/agent/heartbeat` | Agent | heartbeat and observations |
| GET | `/api/v1/tls/allow` | Caddy ask token | authorize certificate hostnames |

The current enrollment request includes `name`, one-time `token`, a random `request_id`, and a client-generated `agent_token`. Repeating the same claim returns the same Client ID; a different request ID cannot reuse a consumed token. The legacy two-field request remains accepted for compatibility.

Expose `/api/v1/tls/allow` to Caddy over loopback only. It permits the management hostname and enabled HTTP routes.

Enrollment tokens last at most 30 days and their plaintext is returned once. Compatibility `/api/v1/admin/*` paths remain available; new integrations should use the canonical paths above.
