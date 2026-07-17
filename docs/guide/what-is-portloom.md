# PortLoom 是什么

PortLoom 是一个自托管的隧道代理。它把家中 NAS 或公司内网里的 Web 服务，通过一台有公网地址的 Docker 主机发布到互联网。

| 位置 | 安装内容 | 负责什么 |
| --- | --- | --- |
| 公网 VPS 或云主机 | PortLoom Server + 受管 sshd | WebUI、管理接口、原生 HTTPS 入口和隧道入口 |
| NAS 或内网服务器 | PortLoom Agent | 主动连接 Server，把流量转发给本地服务 |

Agent 只需要向外访问 Server 的 HTTPS 和 SSH 端口。路由器不需要给 NAS 做端口转发。

```text
浏览器 ──HTTPS──> PortLoom Server 原生入口/Gateway
                              │ 已建立的加密反向隧道
                              ▼
                    PortLoom Agent → 内网服务
```

简易安装不需要 Caddy：Server 自己监听 80/443、通过 autocert HTTP-01 管理证书，并只授权管理域名和已启用的 HTTP 路由域名。

安装后在 WebUI 添加 Agent、执行生成的命令，再创建包含本地地址、端口和公网域名的 HTTP 路由。

当前版本完整管理 HTTP/HTTPS 域名路由。TCP 字段仅为兼容元数据；内置公网入口和 WebUI 不创建公网 TCP 监听。Server 使用单个 SQLite 数据库，不提供多 Server 主动集群。

已有 Caddy、Nginx 或 Nginx Proxy Manager 可以作为使用 8080/8081 的高级兼容入口，参见[反向代理接入](/install/reverse-proxy)。
