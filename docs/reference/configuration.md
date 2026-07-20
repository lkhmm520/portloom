# 配置参考

简易安装器会生成配置；生产环境优先使用安装器参数，而不是手改生成文件。手写 Compose 时，容器内路径必须是绝对路径。

## Server

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TM_ADMIN_TOKEN` | 无 | 必填，至少 16 字符 |
| `TM_LISTEN_ADDR` | `127.0.0.1:8080` | WebUI 与管理 API 的内部监听器 |
| `TM_GATEWAY_ADDR` | `127.0.0.1:8081` | 传统 scheme-agnostic Gateway 上游 |
| `TM_EDGE_HTTP_ADDR` | 空 | 原生主 HTTP/ACME edge；简易安装为 `:80` 或 `--http-port` |
| `TM_EDGE_HTTPS_ADDR` | 空 | 原生主 HTTPS edge；简易安装为 `:443` 或 `--https-port` |
| `TM_TCP_EDGE_BIND_HOST` | `0.0.0.0` | TCP/UDP stream edge 的绑定 IP；字面 IP，`off`/`disabled`/`none` 关闭 stream edge。extra Web 端口改用主 HTTP edge 的 bind host |
| `TM_TLS_CACHE_DIR` | `/data/certs` | autocert 缓存，必须是持久化绝对路径 |
| `TM_ACME_EMAIL` | 空 | 可选 ACME 账户联系邮箱 |
| `TM_PUBLIC_HOST` | 空 | 管理域名；启用原生 edge 或 TLS ask 时必填 |
| `TM_DATABASE_PATH` | `/data/portloom.db` | SQLite 绝对路径 |
| `TM_WEB_DIR` | `/app/web` | WebUI 静态文件目录 |
| `TM_PORT_RANGE_START/END` | `20000` / `29999` | SSH 回环端口池；不得与公网 stream 端口重叠 |
| `TM_ENROLLMENT_TTL` | `1h` | 默认令牌有效期，最大 30 天 |
| `TM_AUTHORIZED_KEYS_PATH` | 空 | 受管 `authorized_keys` 绝对路径 |
| `TM_SSH_HOST_PUBLIC_KEY_PATH` | 空 | 受管 sshd Ed25519 公钥绝对路径 |
| `TM_MANAGED_SSH_PORT` | `2222` | `/system` 与 Agent 命令使用的 SSH 端口 |
| `TM_MANAGED_SSH_ISOLATED` | `false` | 为每个 Agent 使用隔离回环绑定 |
| `TM_TLS_ASK_TOKEN` | 空 | 外部 Caddy on-demand TLS 兼容 Token |
| `TM_TLS_ASK_ADDR` | `127.0.0.1:8082` | ask 专用回环监听器 |

`TM_EDGE_HTTP_ADDR` 与 `TM_EDGE_HTTPS_ADDR` 必须同时设置。原生 edge 还要求有效的 `TM_PUBLIC_HOST` 和绝对 `TM_TLS_CACHE_DIR`。非 root 绑定低端口需 `NET_BIND_SERVICE`。HTTP-01 始终要求**公网 80**最终到达 `TM_EDGE_HTTP_ADDR`，即使本机通过 `--http-port` 使用其他端口。

stream edge 默认开启。其端口不能占用管理/Gateway/TLS ask、主 edge、受管 SSH 或隧道端口池。extra Web 监听器只在原生 edge 启用时可用，并跟随主 HTTP edge 的 bind host。

## Server 安装器

| 参数/环境 | 说明 |
| --- | --- |
| `--domain` | 必填管理域名 |
| `--web-port` / `--ssh-port` | 内部管理端口 / 公网受管 SSH 端口 |
| `--http-port` / `--https-port` | 本机主 edge 端口；公网 80 仍须转发到 HTTP 端口 |
| `--version` | 固定镜像 Tag；生产建议 `0.4.1` 等精确版本 |
| `--disable-tcp-edge` | 写入 `PORTLOOM_TCP_EDGE_BIND_HOST=off` |
| `--enable-tcp-edge` | 兼容参数；v0.4 默认已启用 |
| `PORTLOOM_TCP_EDGE_BIND_HOST` | 与 `--enable-tcp-edge` 同时使用，在首次安装前覆盖 bind IP |
| `PORTLOOM_GATEWAY_PORT` | 设置传统 Gateway 本机端口；升级非默认安装时必须重复原值 |
| `PORTLOOM_HOME` | 安装目录 |

stream-edge 参数可靠地定义首次安装。从已有 `.env` 读取到非空值后，安装器会保留该值；不要把重跑 `--enable/--disable-tcp-edge` 当成已有安装的通用切换器。

## Agent

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TM_SERVER_URL` | 无 | 必填；公网必须 HTTPS，仅 loopback 开发可 HTTP |
| `TM_CLIENT_NAME` / `TM_ENROLLMENT_TOKEN` | 无 | 首次注册；成功后安装器删除一次性令牌 |
| `TM_CLIENT_ID` / `TM_AGENT_TOKEN` | 无 | 注册后的长期身份；通常由 `/data/agent.json` 提供，不应手填或泄露 |
| `TM_AGENT_STATE_PATH` | `/data/agent.json` | 持久身份 |
| `TM_POLL_INTERVAL` | `30s` | 拉取/心跳周期；旧名 `TM_HEARTBEAT_INTERVAL` 兼容 |
| `TM_HEALTH_TIMEOUT` / `TM_REQUEST_TIMEOUT` | `3s` / `10s` | 本地探测/API 超时 |
| `TM_SSH_HOST` / `TM_SSH_PORT` / `TM_SSH_USER` | 无 / `22` / 无 | 简易安装使用公网主机、2222、`tunnel` |
| `TM_SSH_IDENTITY_FILE` | 无 | 私钥；旧名 `TM_SSH_PRIVATE_KEY_PATH` 兼容 |
| `TM_SSH_PUBLIC_KEY_FILE` | 空 | 上传给 Server 的公钥；简易安装器显式设置生成的 `.pub` 路径 |
| `TM_SSH_KNOWN_HOSTS_FILE` | 无 | 固定 Server 主机公钥；旧名 `TM_SSH_KNOWN_HOSTS_PATH` 兼容 |
| `TM_SSH_CONTROL_PATH` | `/tmp/portloom-%C.sock` | ControlMaster socket |
| `TM_SSH_CONNECT_TIMEOUT` | `10` | OpenSSH 连接超时秒数 |
| `TM_MANAGED_SSH_READY_PATH` / `TM_MANAGED_SSH_READY_NONCE` | `/data/managed-ssh.ready` / 空 | 安装器受管 SSH 就绪握手 |
| `TM_MANAGED_SSH_ISOLATED` | `false` | Agent 侧隔离绑定协商；需要公钥文件 |
| `TM_ALLOW_INSECURE_HTTP` | `false` | 只允许 localhost/127.0.0.0/8/`::1` 开发地址 |
