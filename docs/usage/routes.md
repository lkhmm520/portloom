# 路由管理

## 协议类型

| 协议 | 行为 |
| --- | --- |
| HTTPS | 按域名（可选路径前缀）发布，自动申请 ACME 证书，HTTP 请求 308 跳转到 HTTPS |
| HTTP | 按域名（可选路径前缀）在 HTTP 边缘端口明文发布，不申请证书、不跳转 |
| TCP | 在 VPS 上监听指定公网端口，字节级转发到 Agent 本地服务 |
| UDP | 在 VPS 上监听指定公网 UDP 端口，数据报经隧道内中继（长度前缀帧）转发 |

同一域名可以配置多条路由：不同路径前缀（最长前缀优先）、不同公网端口（多个同 scheme 的 Web 路由可共享一个额外端口），HTTP 与 HTTPS 也可并存。管理域名同样可以挂路径前缀路由（仅 HTTPS 默认端口，且不能占用 `/api`、`/assets`、`/healthz`）。

## Web 路由字段

| 字段 | 说明 |
| --- | --- |
| Name | 人类可读名称 |
| Client | 承载该路由的 Agent |
| Domain | 公网 DNS 域名；不能填写 IP、端口、单标签名或纯数字末级域 |
| Path prefix | 可选。以 `/` 开头的路径前缀，如 `/jellyfin`；留空表示整个域名 |
| Strip path | 转发到本地服务前去掉路径前缀（适合不支持子路径的应用） |
| Public port | 可选。留空使用默认 80/443；填写其他端口时，Server 会额外监听该端口 |
| Local host/port | Agent 所在主机可访问的目标 |
| Tunnel group | 当前保存为元数据；连接隔离请用独立 Agent |
| Enabled | 是否进入期望状态、Gateway 选择和原生证书授权 |

TCP/UDP 路由不填域名与路径，`Public port` 为必填。Agent 使用 host network，因此 `127.0.0.1` 表示 NAS 宿主机。若目标是另一个容器，也可填写宿主映射端口或 LAN 地址。

## 域名发布与安全修改

为新域名设置指向 VPS 的 A/AAAA 记录，再启用路由。路由 API 与证书授权使用相同的严格公网 DNS 校验，因此 IP、带端口名称、`localhost` 等无法签发的值会在保存前被拒绝。原生入口只为 **HTTPS** 路由自动申请 ACME HTTP-01 证书；HTTP 路由不产生证书授权。禁用路由会同时使该域名不再获得新的证书授权；已有缓存证书仍保存在 `/data/certs`。

先创建并观察路由，再切换已有公网入口（如有）；删除路由前先恢复旧上游。高流量服务应验证大文件、Range、WebSocket 和长连接，而不只看首页 200。

## TCP / UDP 发布

TCP/UDP 公网发布（stream edge）默认启用，绑定 `0.0.0.0`；设置 `TM_TCP_EDGE_BIND_HOST=off` 可关闭，或改为其他 IP 限制绑定。端口冲突、绑定失败等状态会在 WebUI 的 Public 层显示（`conflict` / `bind_error`）。UDP 会话按公网来源地址区分，空闲 60 秒后回收；封装走 TCP 隧道，适合 DNS、游戏、WireGuard 握手等中小数据报场景。

## 流量与资源监控

仪表盘展示近 60 分钟的总流量曲线、请求数与进出字节合计，以及 Server 与各 Agent 的 CPU / 内存占用（Agent 随心跳上报）。指标保存在内存中，Server 重启后归零。管理 API 为 `GET /api/v1/metrics`。
