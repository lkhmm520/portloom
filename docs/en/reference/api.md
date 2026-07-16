# HTTP API

Administration endpoints require:

```http
Authorization: Bearer <TM_ADMIN_TOKEN>
Content-Type: application/json
```

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/clients` | list clients |
| GET/POST | `/api/v1/enrollment-tokens` | list/issue enrollment tokens |
| GET/POST | `/api/v1/routes` | list/create routes |
| GET/PUT/DELETE | `/api/v1/routes/{id}` | read/update/delete route |
| POST | `/api/v1/agent/enroll` | first enrollment; no Bearer |
| GET | `/api/v1/agent/sync` | fetch desired state |
| POST | `/api/v1/agent/heartbeat` | heartbeat and observations |

```bash
curl -sS -H "Authorization: Bearer $TM_ADMIN_TOKEN"   https://portloom.example.com/api/v1/clients

curl -sS -X POST -H "Authorization: Bearer $TM_ADMIN_TOKEN"   -H 'Content-Type: application/json' -d '{"expires_in":"1h"}'   https://portloom.example.com/api/v1/enrollment-tokens
```

Enrollment tokens cannot exceed 30 days. Legacy `/api/v1/admin/...` aliases remain available, but new integrations should use canonical paths.
