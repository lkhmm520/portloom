# 路由管理

## HTTP 路由字段

| 字段 | 说明 |
| --- | --- |
| Name | 人类可读名称 |
| Client | 承载该路由的 Agent |
| Domain | 公网域名，HTTP 路由必须唯一 |
| Local host/port | Agent 所在主机可访问的目标 |
| Tunnel group | 当前保存为元数据；连接隔离请用独立 Agent |
| Enabled | 是否进入期望状态与 Gateway 选择 |

Agent 使用 host network，因此 `127.0.0.1` 表示 NAS 宿主机。若目标是另一个容器，也可填写宿主映射端口或 LAN 地址。

## 安全修改

先创建并观察路由，再切换现有公网入口（如有）；删除路由前先恢复旧上游。简易安装器自带的Caddy无需逐条修改。高流量服务应验证大文件、Range、WebSocket（若业务需要）和长连接，而不只看首页 200。

## TCP 兼容元数据

WebUI 只允许新建 HTTP 路由。API 或旧版本留下的 TCP 记录仍会显示，但只作为控制平面元数据：内置 Gateway 不创建公网 TCP 监听，Public 状态显示为 `metadata only`，且不会计入 healthy。可删除这类记录，但不能在 WebUI 中新建或编辑。
