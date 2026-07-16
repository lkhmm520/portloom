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

先创建并观察路由，再修改 NPM；删除路由前先把 NPM 切回旧上游。高流量服务应验证大文件、Range、WebSocket（若业务需要）和长连接，而不只看首页 200。

## TCP 路由

当前版本可保存 TCP 路由与公网端口元数据，但 Gateway 仅按 HTTP Host 代理。不要把 TCP 元数据等同于已自动创建公网 TCP 监听。
