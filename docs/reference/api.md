# HTTP API

管理接口使用：

```http
Authorization: Bearer <TM_ADMIN_TOKEN>
Content-Type: application/json
```

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/clients` | 客户端列表 |
| GET | `/api/v1/enrollment-tokens` | 注册令牌元数据 |
| POST | `/api/v1/enrollment-tokens` | 创建一次性令牌 |
| GET/POST | `/api/v1/routes` | 路由列表/创建 |
| GET/PUT/DELETE | `/api/v1/routes/{id}` | 路由详情/更新/删除 |
| POST | `/api/v1/agent/enroll` | Agent首次注册，无Bearer |
| GET | `/api/v1/agent/sync` | Agent拉取期望状态 |
| POST | `/api/v1/agent/heartbeat` | Agent心跳与观测状态 |

## 示例

```bash
curl -sS -H "Authorization: Bearer $TM_ADMIN_TOKEN"   https://portloom.example.com/api/v1/clients

curl -sS -X POST   -H "Authorization: Bearer $TM_ADMIN_TOKEN"   -H 'Content-Type: application/json'   -d '{"expires_in":"1h"}'   https://portloom.example.com/api/v1/enrollment-tokens
```

注册令牌最大30天，请勿记录返回的明文 Token。兼容路径 `/api/v1/admin/routes` 等仍存在，但新集成应使用规范路径。
