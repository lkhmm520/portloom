# 系统架构

```text
                           公网 Docker 主机
浏览器 ──HTTP/HTTPS──> PortLoom Server 原生入口 :80/:443
                                  │
             管理域名 ────────────┼──> WebUI / API / SQLite
             已启用路由域名 ──────└──> Gateway
                                         │ VPS 回环端口
                                         ▼
                                  managed sshd :2222
                                         ▲
                                         │ Agent 主动建立 OpenSSH -R
内网服务 <────────────── Agent <─────────┘
```

简易安装只运行 `portloom-server` 和 `portloom-sshd`。Server 内部仍有管理监听器（默认 `127.0.0.1:8080`）和 Gateway（默认 `127.0.0.1:8081`），同时直接监听公网 80/443；不需要 Caddy、Nginx 或 NPM。

## 管理路径

Server 的 SQLite 保存 Agent、令牌校验值、SSH 公钥、路由、分配端口和状态。WebUI 调用管理 API；Agent 通过 HTTPS 注册、拉取路由和发送心跳。当前设计只有一个 Server 写入者。

## 隧道路径

每个 Agent 维持一个 OpenSSH ControlMaster。路由变更时，Agent 使用 `ssh -O forward/cancel -R` 动态增删反向转发。sshd 只允许回环远程转发。

## 公网请求与证书路径

原生入口按 Host 把 `TM_PUBLIC_HOST` 发送到控制处理器，把其他域名发送到 Gateway。Gateway 只选择已启用且已收敛的 HTTP 路由，再通过 VPS 回环端口和 SSH 隧道到达 Agent 的本地目标。

Server 使用 autocert 和 ACME HTTP-01 在端口 80 完成验证，证书缓存持久化到 `/data/certs`。HostPolicy 只授权 `TM_PUBLIC_HOST` 和当前已启用的 HTTP 路由域名；未知域名不会触发签发，端口 80 也只为这些已拥有域名跳转 HTTPS。容器需要 `NET_BIND_SERVICE` 绑定 80/443。

## 传统/高级入口

已有 Caddy、Nginx 或 NPM 时，可禁用原生 80/443 监听：管理域名代理到 8080，业务域名代理到 8081 并保留 Host。可选的 `/api/v1/tls/allow` 与 `TM_TLS_ASK_*` 仅保留给外部 Caddy on-demand TLS 兼容，不是默认证书路径。

## 故障行为

运行中的 Agent 会在 Server 短暂不可用时保留已有转发并重试；SSH 主连接失效时会重建。主机公钥变化会被严格拒绝。Server 启动时从 SQLite 重建授权文件；取消转发失败时不伪报收敛。
