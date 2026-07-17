# 配置参考

简易安装器会生成这些变量。手写Compose时应保持路径为容器内绝对路径。

## Server

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TM_ADMIN_TOKEN` | 无 | 必填，至少16字符 |
| `TM_LISTEN_ADDR` | `127.0.0.1:8080` | WebUI与管理API |
| `TM_GATEWAY_ADDR` | `127.0.0.1:8081` | HTTP Host Gateway |
| `TM_DATABASE_PATH` | `/data/portloom.db` | SQLite绝对路径 |
| `TM_WEB_DIR` | `/app/web` | WebUI静态资源 |
| `TM_PORT_RANGE_START` | `20000` | SSH回环端口池起点 |
| `TM_PORT_RANGE_END` | `29999` | SSH回环端口池终点 |
| `TM_ENROLLMENT_TTL` | `1h` | 默认注册令牌有效期，最大30天 |
| `TM_AUTHORIZED_KEYS_PATH` | 空 | 受管`authorized_keys`绝对路径 |
| `TM_SSH_HOST_PUBLIC_KEY_PATH` | 空 | 受管sshd的Ed25519主机公钥 |
| `TM_MANAGED_SSH_PORT` | `2222` | WebUI生成Agent命令时使用的SSH端口 |
| `TM_PUBLIC_HOST` | 空 | 简易HTTPS部署的管理域名 |
| `TM_TLS_ASK_TOKEN` | 空 | Caddy按需证书授权Token |

两项受管SSH路径必须同时设置。`TM_PUBLIC_HOST`和`TM_TLS_ASK_TOKEN`也必须同时设置。

## Managed sshd

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORTLOOM_SSH_PORT` | `2222` | 专用SSH监听端口 |

容器路径`/hostkeys`必须可写并持久化；`/auth`应只读挂载Server维护的授权卷。

## Agent

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TM_SERVER_URL` | 无 | 必填，公网部署要求HTTPS |
| `TM_CLIENT_NAME` | 无 | 首次注册名称 |
| `TM_ENROLLMENT_TOKEN` | 无 | 仅首次注册使用 |
| `TM_AGENT_STATE_PATH` | `/data/agent.json` | 持久Agent身份 |
| `TM_POLL_INTERVAL` | `30s` | 拉取和心跳周期 |
| `TM_HEARTBEAT_INTERVAL` | `30s` | 兼容别名 |
| `TM_HEALTH_TIMEOUT` | `3s` | 本地探测超时 |
| `TM_REQUEST_TIMEOUT` | `10s` | API请求超时 |
| `TM_SSH_HOST` | 无 | 受管sshd主机 |
| `TM_SSH_PORT` | `22` | SSH端口；简易安装使用2222 |
| `TM_SSH_USER` | 无 | SSH账户；简易安装使用`tunnel` |
| `TM_SSH_IDENTITY_FILE` | 无 | Agent私钥路径 |
| `TM_SSH_PUBLIC_KEY_FILE` | `<identity>.pub` | 上传给Server的Agent公钥 |
| `TM_SSH_KNOWN_HOSTS_FILE` | 无 | 固定Server主机公钥文件 |
| `TM_SSH_CONTROL_PATH` | `/tmp/portloom-%C.sock` | ControlMaster socket |
| `TM_SSH_CONNECT_TIMEOUT` | `10` | SSH连接超时秒数 |
| `TM_ALLOW_INSECURE_HTTP` | `false` | 只允许回环开发HTTP |
