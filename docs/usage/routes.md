# 路由管理

## 四种协议

| 协议 | 公网行为 |
| --- | --- |
| HTTPS | 按域名、路径和端口发布；自动申请 ACME 证书。若同一 Host 没有匹配的 HTTP 路由，HTTP 请求 308 跳转到 HTTPS |
| HTTP | 在 HTTP edge 明文发布；不申请证书、不强制跳转 |
| TCP | 在指定 VPS 公网 TCP 端口监听，字节级转发到 Agent 本地服务 |
| UDP | 在指定 VPS 公网 UDP 端口监听，数据报通过长度前缀帧在 SSH 隧道内中继 |

旧数据库中的 `http` 路由来自早期版本时，v0.4 启动迁移会把它改成 `https`，保持旧版“自动 TLS”行为。升级后新建的 `http` 才表示真正的明文 HTTP。

## 在 WebUI 创建

1. 打开 **Routes**，点击顶部 **Add route**；新建协议默认是 **HTTPS**。
2. 选择 Client，填写 Name、协议对应的公网字段、Local host/port、Tunnel group 和 Enabled。
3. 保存后等待 Local、Tunnel、Public 收敛；WebUI 每秒刷新一次，心跳发布窗口最长约 90 秒。
4. 使用真实协议做端到端验证；`published` 本身不测试公网 DNS、防火墙或 UDP 目标响应。

编辑既有路由时 WebUI 会锁定 Client，不能直接把路由转移给另一个 Agent。需要迁移 Client 时先记录配置并规划删除/重建切换窗口，避免同一公网端点的唯一性冲突。

## Web 路由

| 字段 | 说明 |
| --- | --- |
| Domain | 公网 DNS 域名；拒绝 IP、带端口、单标签名和纯数字末级域 |
| Path prefix | 可选；`/` 等同留空。必须以 `/` 开头，不能含空段、`.` 或 `..` 段 |
| Strip path | 转发前移除前缀；必须先填写 Path prefix |
| Public port | 留空使用该协议主 edge；显式 HTTP 80 / HTTPS 443 也会规范化为主 edge。其他允许的 1–65535 端口会动态打开额外 TCP listener |
| Local host/port | Agent 主机可访问的目标；Agent 使用 host network，`127.0.0.1` 指 NAS 宿主机 |
| Tunnel group | 当前仅为元数据；需要连接隔离时使用独立 Agent |

同一 Web 端点由 `(协议, 域名, 公网端口, 路径前缀)` 唯一标识。请求匹配时最长路径前缀优先。多个相同协议的路由可共享同一个额外端口；同一端口不能同时由 HTTP 与 HTTPS extra edge 使用，API/WebUI 会以 409 conflict 拒绝保存。`conflict` Public 状态是对旧库或异常不一致状态的防御性报告。

例如 `/media` 只匹配 `/media` 与 `/media/...`，不会误匹配 `/mediabox`。开启 Strip path 后，`/media/tv` 转发为 `/tv`，正好访问 `/media` 时转发为 `/`。`/` 前缀会规范化为留空，即匹配整个域名。

管理域名可共享 HTTPS 主端口上的非空路径前缀，但不能使用 HTTP、自定义端口、根路径，或覆盖 `/api`、`/assets`、`/healthz`。

::: tip 子路径应用
`Strip path` 只改转发给上游的 URL 路径，不会自动重写响应中的绝对 URL、Cookie Path 或前端资源地址。应用本身不支持子路径时，优先使用独立域名。
:::

## TCP / UDP 路由

TCP/UDP 不填写域名、路径和 Strip path，Public port 必填。同一个 TCP/UDP 公网端口只能属于一条 stream 路由，也不能占用管理口、Gateway、SSH 入口、HTTP/HTTPS edge 或 `TM_PORT_RANGE_START..END` 的隧道回环端口池。

stream edge 默认绑定 `0.0.0.0`。设置 `TM_TCP_EDGE_BIND_HOST=off` 可关闭，或设置为一个字面 IP（如 `127.0.0.1`、`::`）限制监听。关闭时 WebUI 禁止新建 TCP/UDP 路由。

UDP 会话按公网来源地址区分，空闲 60 秒后回收。内部两字节帧长度字段上限是 65535，但这**不是**可承诺的公网 UDP payload：普通 IPv4/IPv6 UDP 上限通常分别为 65507/65527 字节，还会受路径 MTU 与系统 socket 限制。它适合 DNS、游戏控制流、WireGuard 握手等中小数据报，不适合追求原生 UDP 吞吐或抗队头阻塞的场景。stream manager 当前固定上限为全局 1024、每路由 128 个活动会话/连接，没有公开配置项。

## 状态、DNS 与防火墙

- `waiting_agent`：Agent 未收敛、Tunnel 未 up 或心跳已超过 90 秒；
- `pending`：动态监听仍在协调；
- `published`：PortLoom 已建立对应公网处理路径；
- `conflict` / `bind_error`：端口被不同 scheme/路由争用，或操作系统绑定失败；
- `disabled`：路由未启用。

`published` 不验证外部 DNS 或云防火墙。为 Web 域名配置 A/AAAA；为自定义 Web/TCP 端口放行 TCP，为 UDP 路由放行 UDP。HTTPS 只为已启用 HTTPS 域名授权证书；禁用路由不会删除 `/data/certs` 中的缓存证书。

## 流量与资源监控

Dashboard 曲线显示最近 60 个一分钟桶的入站与出站字节之和；Requests、Bytes In、Bytes Out 是 Server 启动以来累计值。TCP 连接和 UDP 会话各计为一次 request/event。资源主值是 PortLoom 进程 CPU 与 RSS：单核满载约为 CPU 100%，内存括号百分比是主机/容器命名空间的总内存使用率，而不是进程 RSS 百分比。

`GET /api/v1/metrics` 另返回每路由累计计数。指标仅在内存中，Server 重启后归零；当前删除路由不会立即清除 metrics API 中已累计的该 route ID 计数。Agent 资源随同步/心跳上报，当前 UI 不标记资源样本是否过期。
