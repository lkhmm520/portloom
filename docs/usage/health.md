# 健康状态

PortLoom 不把所有状态压成一个“绿色圆点”。每条路由应分别理解 Local、Tunnel、Public 与 Revision。

## Local

对 HTTP/HTTPS/TCP，Agent 能否在 `TM_HEALTH_TIMEOUT` 内连接 `local_host:local_port`。Local down 通常是目标未监听、地址/端口错误、容器网络或 NAS 防火墙问题。UDP 无法可靠主动探测目标，Local up 只表示 Agent 侧 UDP relay 已创建；必须另做端到端数据报测试。

## Tunnel

OpenSSH ControlMaster 是否有效，分配的 VPS 回环 `-R` 是否建立。UDP 路由同样依赖一个 TCP 反向转发作为数据报中继。Tunnel down 常见于私钥权限、固定主机公钥不匹配、受管 SSH 策略或回环端口冲突。

## Revision 与心跳

API 写入会增加期望 revision。Agent 必须回报与当前 desired revision **相等**的 observed revision，而且最后心跳在 90 秒内，路由才具备发布条件。落后表示尚未收敛；超前值会被 API 拒绝。`waiting_agent` 往往表示 revision、Tunnel 或心跳其中之一未满足。

## Public

| 状态 | 含义 |
| --- | --- |
| `published` | Gateway 或动态端口监听已就绪；不代表 DNS/防火墙/TLS 从外网一定可达 |
| `waiting_agent` | Agent 未收敛、Tunnel 未 up 或心跳过期 |
| `pending` | TCP/UDP 或 extra web listener 正在协调 |
| `conflict` | 同一端口被不兼容路由占用 |
| `bind_error` | 操作系统无法绑定公网端口，常见于已有进程占用或权限不足 |
| `disabled` / `*_edge_disabled` | 路由或对应 edge 已关闭 |

## 正确排查顺序

1. 在 NAS 上直接访问本地目标；
2. 查看 Agent 安装/运行日志与 Local；
3. 检查心跳、revision、VPS 回环端口和 Tunnel；
4. 对 Web 路由用正确 scheme、端口、Host、路径测试 Gateway/edge；
5. 对 TCP/UDP 从外网进行协议级测试；
6. 最后检查 DNS、ACME、云防火墙、NAT 与运营商策略。
