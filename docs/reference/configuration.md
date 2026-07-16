# 配置参考

## Server

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TM_ADMIN_TOKEN` | 无 | 必填，至少 16 字符 |
| `TM_LISTEN_ADDR` | `127.0.0.1:8080` | 控制台与 API |
| `TM_GATEWAY_ADDR` | `127.0.0.1:8081` | HTTP Host 网关 |
| `TM_DATABASE_PATH` | `/data/portloom.db` | SQLite 绝对路径 |
| `TM_WEB_DIR` | `/app/web` | 管理界面静态资源 |
| `TM_PORT_RANGE_START` | 代码默认 | SSH回环端口池起点 |
| `TM_PORT_RANGE_END` | 代码默认 | SSH回环端口池终点 |
| `TM_ENROLLMENT_TTL` | `1h` | 默认注册令牌有效期，最大30天 |

## Agent

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TM_SERVER_URL` | 无 | 必填，默认要求 HTTPS |
| `TM_CLIENT_NAME` | 无 | 首次注册名称 |
| `TM_ENROLLMENT_TOKEN` | 无 | 仅首次注册使用 |
| `TM_AGENT_STATE_PATH` | `/data/agent.json` | 持久化身份 |
| `TM_POLL_INTERVAL` | `30s` | 拉取/心跳周期 |
| `TM_HEARTBEAT_INTERVAL` | `30s` | `TM_POLL_INTERVAL`兼容别名 |
| `TM_HEALTH_TIMEOUT` | `3s` | 本地探测超时 |
| `TM_REQUEST_TIMEOUT` | `10s` | 控制平面请求超时 |
| `TM_SSH_HOST` | 无 | SSH服务器 |
| `TM_SSH_PORT` | `22` | SSH端口 |
| `TM_SSH_USER` | 无 | 专用账户 |
| `TM_SSH_IDENTITY_FILE` | 无 | 容器内私钥路径 |
| `TM_SSH_KNOWN_HOSTS_FILE` | 无 | 固定主机指纹文件 |
| `TM_SSH_CONTROL_PATH` | `/tmp/portloom-%C.sock` | ControlMaster socket |
| `TM_SSH_CONNECT_TIMEOUT` | `10` | SSH连接超时秒数 |
| `TM_ALLOW_INSECURE_HTTP` | `false` | 仅允许本机开发HTTP |
