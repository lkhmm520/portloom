# HTTP API

管理员接口使用 `Authorization: Bearer <admin-token>`；Agent 接口使用注册后的长期 Agent Token。请求体上限 1 MiB，JSON 拒绝未知字段和多个顶层值。

## 端点

| 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- |
| GET | `/healthz` | 无 | 控制面进程存活检查，返回 `{"status":"ok"}` |
| GET | `/api/v1/system` | 管理员 | 版本、受管 SSH、stream edge、extra Web 端口能力 |
| GET | `/api/v1/metrics` | 管理员 | 流量、Server 与 Agent 资源快照 |
| GET | `/api/v1/clients` | 管理员 | Agent/Client 列表 |
| GET/POST | `/api/v1/enrollment-tokens` | 管理员 | 列表 / 创建一次性令牌 |
| DELETE | `/api/v1/enrollment-tokens/{id}` | 管理员 | 删除/撤销令牌记录，不影响已注册 Agent |
| GET/POST | `/api/v1/routes` | 管理员 | 路由列表 / 创建 |
| GET/PUT/DELETE | `/api/v1/routes/{id}` | 管理员 | 路由详情 / 完整更新 / 删除 |
| POST | `/api/v1/agent/enroll` | 一次性令牌 Claim | 首次注册 |
| PUT | `/api/v1/agent/ssh-key` | Agent | 注册/更新 Ed25519 公钥 |
| GET | `/api/v1/agent/desired` | Agent | 内置 v0.4 Agent 使用：拉取 Agent、revision 与期望路由 |
| POST | `/api/v1/agent/observed` | Agent | 内置 v0.4 Agent 使用：按当前 wire schema 上报 revision、路由观测和可选 system 资源 |
| GET | `/api/v1/agent/sync` | Agent | 与 `desired` 使用同一 handler、响应相同的兼容别名 |
| POST | `/api/v1/agent/heartbeat` | Agent | 旧版兼容上报端点；语义相近，但请求 schema 与 `observed` 不兼容 |
| GET | `/api/v1/tls/allow` | Caddy ask Token | 独立回环监听器上的兼容 TLS 授权 |

## 路由 JSON

创建/更新接受这些核心字段：

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

`protocol` 为 `http|https|tcp|udp`。Web 路由必须有有效 DNS 域名，`public_port: 0`/省略表示主 edge；TCP/UDP 必须省略域名/路径并给出 1–65535 公网端口。响应还包含自动分配的 `remote_port`、revision、Local/Tunnel、`agent_last_seen_at` 和 `public_status`。

创建/更新时，占用系统/监听器保留端口返回 `409 {"error":"reserved_tcp_port"}`；路由之间的公网端口、跨 scheme/类型占用和数据库唯一性冲突返回 `409 {"error":"conflict"}`。`tcp_edge_disabled`、`udp_edge_disabled`、`web_port_edge_disabled` 和 `reserved_domain` 也会拒绝写入。`public_status=conflict` 主要用于防御性呈现既有的不一致状态。

## `/system` 与 `/metrics`

`/system` 返回 `managed_ssh`、`version`、`tcp_edge`、`udp_edge`、`web_port_edge`；启用受管 SSH 时还有 `ssh_port`、`ssh_host_key`，启用 stream edge 时还有 `tcp_bind_host`。

`/metrics` 的 `traffic` 包含：

- `total`: `{requests, bytes_in, bytes_out}`；
- `routes`: 以 route ID 为键的累计计数；
- `series`: 60 个按分钟排列的 `{t, requests, bytes_in, bytes_out}` 样本。

`server` 与 `agents` 包含 `cpu_percent`、`rss_bytes`、`mem_total_bytes`、`mem_available_bytes`；Agent 记录另有 `reported_at`。CPU 的 100% 表示占满一个核心。所有指标在内存中。

## 注册与兼容

创建令牌可传 `{"expires_in":"24h"}` 或 `{"expires_in_seconds":86400}`，两者不能同时出现，最大 30 天。明文 Token 只在创建响应中返回。

当前注册 Claim 包含 `name`、`token`、随机 `request_id` 和客户端生成的 `agent_token`。相同 Claim 重试返回同一 Client；不同请求 ID 不能复用已消费令牌。

兼容的 `/api/v1/admin/*` 路由仍保留。内置 v0.4 Agent 当前调用 `desired/observed`；`desired` 与 `sync` 是相同 GET handler 的别名，但两个 POST 端点不能复用 JSON。

当前 `POST /api/v1/agent/observed`：

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

旧版兼容 `POST /api/v1/agent/heartbeat`：

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

两者都可带可选 `system` 对象，但字段名、全局/逐路由 revision 转换不同。JSON 严格拒绝未知字段，混用 payload 会返回 `400 invalid_request`。`/api/v1/tls/allow` 只授权管理域名和已启用 **HTTPS** 路由域名，并且只应监听回环地址。
