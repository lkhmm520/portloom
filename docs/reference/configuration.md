# 配置参考

简易安装器会生成这些变量。手写 Compose 时应保持路径为容器内绝对路径。

## Server

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TM_ADMIN_TOKEN` | 无 | 必填，至少 16 字符 |
| `TM_LISTEN_ADDR` | `127.0.0.1:8080` | WebUI 与管理 API 的内部/传统上游监听器 |
| `TM_GATEWAY_ADDR` | `127.0.0.1:8081` | HTTP Host Gateway 的内部/传统上游监听器 |
| `TM_EDGE_HTTP_ADDR` | 空 | 原生公网 HTTP/ACME 监听；简易安装使用 `:80` |
| `TM_EDGE_HTTPS_ADDR` | 空 | 原生公网 HTTPS 监听；简易安装使用 `:443` |
| `TM_TLS_CACHE_DIR` | `/data/certs` | autocert 证书缓存绝对路径，必须持久化 |
| `TM_ACME_EMAIL` | 空 | 可选 ACME 账户联系邮箱 |
| `TM_PUBLIC_HOST` | 空 | 管理域名；启用原生入口时必填 |
| `TM_DATABASE_PATH` | `/data/portloom.db` | SQLite 绝对路径 |
| `TM_WEB_DIR` | `/app/web` | WebUI 静态资源 |
| `TM_PORT_RANGE_START` / `TM_PORT_RANGE_END` | `20000` / `29999` | SSH 回环端口池 |
| `TM_ENROLLMENT_TTL` | `1h` | 默认注册令牌有效期，最大 30 天 |
| `TM_AUTHORIZED_KEYS_PATH` | 空 | 受管 `authorized_keys` 绝对路径 |
| `TM_SSH_HOST_PUBLIC_KEY_PATH` | 空 | 受管 sshd Ed25519 主机公钥 |
| `TM_MANAGED_SSH_PORT` | `2222` | 生成 Agent 命令时使用的 SSH 端口 |
| `TM_MANAGED_SSH_ISOLATED` | `false` | 使用受管 SSH 路径时隔离 Agent 转发 |
| `TM_TLS_ASK_TOKEN` | 空 | 可选外部 Caddy on-demand TLS 兼容 Token |
| `TM_TLS_ASK_ADDR` | `127.0.0.1:8082` | 可选 `ask` 端点的回环监听地址 |

`TM_EDGE_HTTP_ADDR` 与 `TM_EDGE_HTTPS_ADDR` 必须同时设置。原生模式还要求 `TM_PUBLIC_HOST` 和绝对的 `TM_TLS_CACHE_DIR`。`TM_PUBLIC_HOST` 必须是至少两段、末段包含字母的 DNS 名称；不接受 IP、缩写 IP、纯整数或 `localhost`。非 root 容器绑定 80/443 需要 `NET_BIND_SERVICE` capability。

原生入口使用 autocert HTTP-01，只授权 `TM_PUBLIC_HOST` 和当前已启用的 HTTP 路由域名。`TM_TLS_ASK_*` 不参与原生签发，只用于外部 Caddy 兼容；设置 Token 时要求 `TM_PUBLIC_HOST`，且监听地址必须使用回环 IP。

两项受管 SSH 路径必须同时设置；`TM_MANAGED_SSH_ISOLATED=true` 还要求两项路径均已配置。

## Managed sshd

`PORTLOOM_SSH_PORT` 默认 `2222`。`/hostkeys` 必须可写并持久化；`/auth` 应只读挂载 Server 维护的授权卷。

## Agent

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TM_SERVER_URL` | 无 | 必填，公网部署要求 HTTPS |
| `TM_CLIENT_NAME` / `TM_ENROLLMENT_TOKEN` | 无 | 首次注册名称和一次性令牌 |
| `TM_AGENT_STATE_PATH` | `/data/agent.json` | 持久 Agent 身份 |
| `TM_POLL_INTERVAL` | `30s` | 拉取和心跳周期 |
| `TM_HEALTH_TIMEOUT` / `TM_REQUEST_TIMEOUT` | `3s` / `10s` | 本地探测/API 超时 |
| `TM_SSH_HOST` / `TM_SSH_PORT` / `TM_SSH_USER` | 无 / `22` / 无 | 简易安装使用公网主机、2222、`tunnel` |
| `TM_SSH_IDENTITY_FILE` | 无 | Agent 私钥路径 |
| `TM_SSH_PUBLIC_KEY_FILE` | `<identity>.pub` | 上传给 Server 的 Agent 公钥 |
| `TM_SSH_KNOWN_HOSTS_FILE` | 无 | 固定 Server 主机公钥文件 |
| `TM_SSH_CONTROL_PATH` | `/tmp/portloom-%C.sock` | ControlMaster socket |
| `TM_ALLOW_INSECURE_HTTP` | `false` | 只允许回环开发 HTTP |
