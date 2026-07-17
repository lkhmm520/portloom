# HTTP API

管理员接口使用`Authorization: Bearer <admin-token>`。Agent接口使用注册后得到的Agent Token。

| 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/system` | 管理员 | 受管SSH状态、端口和Server主机公钥 |
| GET | `/api/v1/clients` | 管理员 | Agent列表 |
| GET/POST | `/api/v1/enrollment-tokens` | 管理员 | 令牌列表/创建一次性令牌 |
| GET/POST | `/api/v1/routes` | 管理员 | 路由列表/创建 |
| GET/PUT/DELETE | `/api/v1/routes/{id}` | 管理员 | 路由详情/更新/删除 |
| POST | `/api/v1/agent/enroll` | 一次性令牌 | Agent首次注册 |
| PUT | `/api/v1/agent/ssh-key` | Agent | 注册或更新Agent Ed25519公钥 |
| GET | `/api/v1/agent/sync` | Agent | 拉取期望路由 |
| POST | `/api/v1/agent/heartbeat` | Agent | 心跳和观测状态 |
| GET | `/api/v1/tls/allow` | Caddy ask Token | 判断域名是否可申请证书 |

当前注册请求包含`name`、一次性`token`、随机`request_id`和客户端生成的`agent_token`。相同 Claim 重试会返回同一 Client ID；不同请求 ID 不能复用已消费令牌。旧的两字段请求仅作为兼容路径保留。

`/api/v1/tls/allow`只应通过回环地址供Caddy调用。它只允许管理域名和已启用的HTTP路由。

注册令牌最多30天，明文只返回一次。兼容的`/api/v1/admin/*`路径仍存在，新集成应使用表中的规范路径。
