# HTTP API

管理员接口使用 `Authorization: Bearer <admin-token>`；Agent 接口使用注册后获得的 Agent Token。

| 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/system` | 管理员 | 受管 SSH 状态、端口和 Server 主机公钥 |
| GET | `/api/v1/clients` | 管理员 | Agent 列表 |
| GET/POST | `/api/v1/enrollment-tokens` | 管理员 | 令牌列表/创建一次性令牌 |
| GET/POST | `/api/v1/routes` | 管理员 | 路由列表/创建 |
| GET/PUT/DELETE | `/api/v1/routes/{id}` | 管理员 | 路由详情/更新/删除 |
| POST | `/api/v1/agent/enroll` | 一次性令牌 | Agent 首次注册 |
| PUT | `/api/v1/agent/ssh-key` | Agent | 注册或更新 Agent Ed25519 公钥 |
| GET | `/api/v1/agent/sync` | Agent | 拉取期望路由 |
| POST | `/api/v1/agent/heartbeat` | Agent | 心跳和观测状态 |
| GET | `/api/v1/tls/allow` | 外部 Caddy ask Token | 旧式 on-demand TLS 域名授权 |

原生 HTTPS 入口不调用 `/api/v1/tls/allow`。Server 内部 autocert HostPolicy 直接只允许 `TM_PUBLIC_HOST` 和已启用的 HTTP 路由域名。

`/api/v1/tls/allow` 仅为外部 Caddy 兼容保留。启用 `TM_TLS_ASK_TOKEN` 后，应只通过 `TM_TLS_ASK_ADDR` 的回环监听器调用；不要把端点或 Token 暴露到公网。它采用与原生入口相同的域名范围：管理域名和已启用 HTTP 路由。

当前注册请求包含 `name`、一次性 `token`、随机 `request_id` 和客户端生成的 `agent_token`。相同 Claim 重试会返回同一 Client ID；不同请求 ID 不能复用已消费令牌。旧的两字段请求仅作为兼容路径保留。

注册令牌最多 30 天，明文只返回一次。兼容的 `/api/v1/admin/*` 路径仍存在，新集成应使用表中的规范路径。
