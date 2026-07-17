# 路由管理

## HTTP 路由字段

| 字段 | 说明 |
| --- | --- |
| Name | 人类可读名称 |
| Client | 承载该路由的 Agent |
| Domain | 唯一的公网 DNS 域名；不能填写 IP、端口、单标签名或纯数字末级域 |
| Local host/port | Agent 所在主机可访问的目标 |
| Tunnel group | 当前保存为元数据；连接隔离请用独立 Agent |
| Enabled | 是否进入期望状态、Gateway 选择和原生证书授权 |

Agent 使用 host network，因此 `127.0.0.1` 表示 NAS 宿主机。若目标是另一个容器，也可填写宿主映射端口或 LAN 地址。

## 域名发布与安全修改

为新域名设置指向 VPS 的 A/AAAA 记录，再启用路由。路由 API 与证书授权使用相同的严格公网 DNS 校验，因此 IP、带端口名称、`localhost` 等无法签发的值会在保存前被拒绝。原生入口会自动将域名加入 HTTPS 授权并通过 ACME HTTP-01 获取证书，无需逐条修改 Caddy 或其他代理。禁用路由会同时使该域名不再获得新的证书授权；已有缓存证书仍保存在 `/data/certs`。

先创建并观察路由，再切换已有公网入口（如有）；删除路由前先恢复旧上游。高流量服务应验证大文件、Range、WebSocket 和长连接，而不只看首页 200。

## TCP 兼容元数据

WebUI 只允许新建 HTTP 路由。API 或旧版本留下的 TCP 记录仍会显示，但只作为控制平面元数据：内置 Gateway 和原生入口都不创建公网 TCP 监听，Public 显示 `metadata only`，且不会计入 healthy。
