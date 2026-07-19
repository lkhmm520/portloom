# 系统架构

```text
                        公网 Docker 主机
 Web ──HTTP/HTTPS──> primary/extra Web edge ── Gateway ──┐
 TCP/UDP ─────────> stream edge ─────────────────────────┤
                                                          │ 按路由分配的回环端口
                                                          ▼
                                                   managed sshd :2222
                                                          ▲
                                                          │ Agent 主动 OpenSSH -R
 内网 HTTP/TCP/UDP 服务 <────────────── Agent <───────────┘
```

简易安装只运行 `portloom-server` 与 `portloom-sshd`。Server 进程同时承载控制面、Web Gateway、主 HTTP/HTTPS edge、动态 extra Web listener、TCP/UDP stream edge、SQLite 和内存指标。

## 控制面

管理监听器默认 `127.0.0.1:8080`，提供 WebUI、`/api/v1/*` 和 `/healthz`。SQLite 保存 Agent、令牌校验值、SSH 公钥、路由、分配的回环端口、revision 与最后观测状态。管理员与 Agent 分别使用 Bearer Token。

Server 启动时执行向前兼容的 SQLite schema 迁移。v0.4 会增加路径字段/索引，并把早期版本的 legacy `http` 路由迁移为 `https`，保持旧版自动 TLS 行为。当前只有一个 Server 写入者。

## Web 数据面

- 主 HTTP/HTTPS edge 默认监听 80/443，也可绑定本机其他端口；默认端口路由跟随主 edge，而不是硬编码只在 80/443 匹配。
- extra Web manager 每秒协调带 `public_port` 的路由；同一端口只能运行一个 scheme，但该 scheme 下可按 Host/路径承载多条路由。
- Gateway 先匹配 scheme/端口/Host，再以最长路径前缀选择已收敛路由。`strip_path` 只重写上游请求路径；原 Host 保留，`X-Forwarded-*` 根据可信的请求来源、Host 和入口 scheme 重建，不信任客户端注入值。
- 管理域名由原生 router 优先保护控制路径，并允许安全的 HTTPS 子路径路由。

HTTPS 使用 autocert 和 ACME HTTP-01，证书缓存在 `/data/certs`。HostPolicy 只授权管理域名和当前已启用 HTTPS 路由域名。公网 80 必须能到达主 HTTP edge。

## SSH 与 stream 数据面

每个 Agent 使用一个 OpenSSH ControlMaster。每条路由在 VPS 获得一个回环端口，Agent 通过 `ssh -O forward/cancel -R` 动态增删转发。受管 sshd 只允许回环远程转发；启用隔离绑定时，Server 为不同 Agent 使用不同回环地址。

TCP edge 将公网连接转到该回环端口。UDP edge 按公网来源建立会话：VPS 将数据报编码为长度前缀帧，经回环 TCP/SSH 转发到 Agent 的本地 UDP relay，再发送给真实 UDP 服务；空闲 60 秒回收。

## 状态与指标

Agent 心跳包含全局 revision、每路由 Local/Tunnel 观测和进程资源。发布就绪要求路由启用、Tunnel up、observed revision 与当前 desired revision **相等**且心跳不超过 90 秒；落后表示未收敛，超前值会被 API 拒绝。动态 listener 另外报告 pending/conflict/bind_error/published。

Server 在内存中记录 Web 请求或 stream 会话的请求数与双向字节，并保留 60 个分钟样本。CPU 是进程相邻采样间的使用率，100% 代表一个核心；RSS 是进程常驻内存。Server 重启后指标清零。

## 外部入口兼容路径

禁用原生 edge 时，8080 作为管理上游，8081 作为传统 scheme-agnostic Web Gateway。外部代理负责 TLS、跳转和公网端口；自定义 Web public port 不可用。TCP/UDP stream edge 独立于原生 Web edge，可继续运行。
