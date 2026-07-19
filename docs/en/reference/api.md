# HTTP API

Admin endpoints use `Authorization: Bearer <admin-token>`. Agent endpoints use the long-lived Agent token created during enrollment. Request bodies are limited to 1 MiB; JSON rejects unknown fields and multiple top-level values.

## Endpoints

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| GET | `/healthz` | none | control-plane liveness; returns `{"status":"ok"}` |
| GET | `/api/v1/system` | admin | version, managed SSH, stream-edge, and extra-web-port capabilities |
| GET | `/api/v1/metrics` | admin | traffic and Server/Agent resource snapshot |
| GET | `/api/v1/clients` | admin | Agent/Client list |
| GET/POST | `/api/v1/enrollment-tokens` | admin | list / create one-time tokens |
| DELETE | `/api/v1/enrollment-tokens/{id}` | admin | delete/revoke token record without affecting an enrolled Agent |
| GET/POST | `/api/v1/routes` | admin | list / create routes |
| GET/PUT/DELETE | `/api/v1/routes/{id}` | admin | get / replace / delete route |
| POST | `/api/v1/agent/enroll` | one-time claim | first enrollment |
| PUT | `/api/v1/agent/ssh-key` | Agent | register/update Ed25519 public key |
| GET | `/api/v1/agent/desired` | Agent | used by the built-in v0.4 Agent to pull Agent, revision, and desired routes |
| POST | `/api/v1/agent/observed` | Agent | Built-in v0.4 Agent: report revision, route observations, and optional system resources using the current wire schema |
| GET | `/api/v1/agent/sync` | Agent | Compatibility alias using the same handler and response as `desired` |
| POST | `/api/v1/agent/heartbeat` | Agent | Legacy compatibility report endpoint; similar purpose, but a wire schema incompatible with `observed` |
| GET | `/api/v1/tls/allow` | Caddy ask token | compatibility TLS authorization on a separate loopback listener |

## Route JSON

Create/update accepts these core fields:

```json
{
  "client_id": "client-id",
  "name": "media",
  "protocol": "https",
  "domain": "media.example.com",
  "path_prefix": "/jellyfin",
  "strip_path": true,
  "public_port": 8443,
  "local_host": "127.0.0.1",
  "local_port": 8096,
  "tunnel_group": "web",
  "enabled": true
}
```

`protocol` is `http|https|tcp|udp`. Web routes require a valid DNS hostname; omitted/zero `public_port` means the primary edge. TCP/UDP omit domain/path and require a 1–65535 public port. Responses add allocated `remote_port`, revisions, Local/Tunnel, `agent_last_seen_at`, and `public_status`.

At create/update time, use of a reserved system/listener port returns `409 {"error":"reserved_tcp_port"}`. Public-port conflicts between routes, cross-scheme/type occupation, and database uniqueness conflicts return `409 {"error":"conflict"}`. `tcp_edge_disabled`, `udp_edge_disabled`, `web_port_edge_disabled`, and `reserved_domain` also reject writes. `public_status=conflict` is mainly a defensive representation of an existing inconsistent state.

## `/system` and `/metrics`

`/system` returns `managed_ssh`, `version`, `tcp_edge`, `udp_edge`, and `web_port_edge`; managed SSH adds `ssh_port`/`ssh_host_key`, and stream edge adds `tcp_bind_host`.

`/metrics.traffic` contains:

- `total`: `{requests, bytes_in, bytes_out}`;
- `routes`: cumulative counters keyed by route ID;
- `series`: 60 minute-ordered `{t, requests, bytes_in, bytes_out}` samples.

`server` and `agents` contain `cpu_percent`, `rss_bytes`, `mem_total_bytes`, and `mem_available_bytes`; Agent rows also include `reported_at`. CPU 100% means one full core. All metrics are in memory.

## Enrollment and compatibility

Token creation accepts either `{"expires_in":"24h"}` or `{"expires_in_seconds":86400}`, not both, with a 30-day maximum. Plaintext appears only in the create response.

The current enrollment claim includes `name`, `token`, random `request_id`, and client-generated `agent_token`. Retrying the same claim returns the same Client; another request ID cannot reuse a consumed token.

Compatible `/api/v1/admin/*` routes remain. Built-in v0.4 Agent calls `desired/observed`. `desired` and `sync` are aliases for the same GET handler, but the two POST endpoints cannot reuse JSON.

Current `POST /api/v1/agent/observed`:

```json
{
  "revision": 12,
  "routes": [{
    "route_id": "route-id",
    "local_status": "up",
    "tunnel_status": "up",
    "error": ""
  }]
}
```

Legacy-compatible `POST /api/v1/agent/heartbeat`:

```json
{
  "observed_revision": 12,
  "routes": [{
    "route_id": "route-id",
    "observed_revision": 12,
    "local_status": "up",
    "tunnel_status": "up",
    "last_error": ""
  }]
}
```

Both may carry an optional `system` object, but field names and global/per-route revision conversion differ. JSON strictly rejects unknown fields, so mixing payloads returns `400 invalid_request`. `/api/v1/tls/allow` authorizes only the management hostname and enabled **HTTPS** route hostnames and should listen on loopback only.
