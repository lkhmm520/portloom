# 认识 PortLoom

PortLoom 是一个面向 NAS、家庭实验室和小型自托管环境的反向 SSH 隧道控制平面。公网主机运行 Server 与受管 sshd；内网主机运行 Agent，并主动向外建立加密连接，因此 NAS 路由器无需开放入站端口。

| 位置 | 组件 | 职责 |
| --- | --- | --- |
| 公网 VPS / 云主机 | PortLoom Server + 受管 sshd | WebUI、API、原生 HTTP/HTTPS 入口、TCP/UDP 监听和 SSH 隧道入口 |
| NAS / 内网服务器 | PortLoom Agent | 拉取期望路由、探测本地服务、维护反向转发并上报状态/资源 |

```text
Web 请求 ──HTTP/HTTPS──> Server 原生入口 ──Gateway──┐
TCP/UDP 客户端 ───────> Server stream edge ───────┤
                                                     │ VPS 回环端口
                                                     ▼
                                              managed sshd :2222
                                                     ▲
                                                     │ Agent 主动建立 OpenSSH -R
内网服务 <────────────────────────────── Agent <─────┘
```

## v0.4 可以做什么

- **HTTPS**：按域名和可选路径前缀发布，自动申请 ACME 证书；没有匹配 HTTP 路由时，同域名 HTTP 请求会 308 跳转到 HTTPS。
- **HTTP**：明文发布，不申请证书、不强制跳转。
- **TCP / UDP**：在 VPS 指定公网端口监听并转发；UDP 数据报经 SSH 隧道内的长度前缀帧中继。
- **多路由复用**：同一域名可按路径前缀或自定义公网端口区分；管理域名也可挂安全的 HTTPS 路径路由。
- **可观测性**：控制台展示三层健康状态、近 60 分钟流量总量，以及 Server/Agent 的 CPU 与内存；metrics API 另提供每路由计数。
- **安全注册**：Add Agent 生成带一次性令牌和固定 SSH 主机公钥的安装命令；未使用令牌可在控制台撤销。

默认安装不需要 Caddy、Nginx 或 NPM。Server 自己监听 80/443 并使用 autocert HTTP-01 管理证书；已有外部入口仍可通过 8080/8081 兼容接入。

## 当前边界

- Server 使用单个 SQLite 数据库，预期单实例运行，不支持 active/active 写入。
- 每个 Agent 当前使用一个 OpenSSH ControlMaster；`tunnel_group` 仍是元数据，需要连接隔离时运行多个 Agent/Client。
- UDP 通过 TCP 型 SSH 隧道封装，适合中小数据报，不等同于原生 UDP 性能。
- 流量和资源指标保存在内存中，Server 重启后归零。
