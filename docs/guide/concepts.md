# 核心概念

## Server、Gateway 与公网入口

同一 Server 进程包含管理监听器（默认 `127.0.0.1:8080`）和传统 Gateway（默认 `127.0.0.1:8081`）。简易安装还启用原生 HTTP/HTTPS 入口：主监听端口默认是 80/443，也可通过安装器改成本机其他端口。

Gateway 根据进入请求的 **scheme、端口、Host 和路径** 选择已启用且已收敛的 Web 路由。路径采用最长前缀优先；`strip_path` 可在转发前移除匹配前缀。非默认 Web 公网端口由 Server 动态创建额外监听器。

TCP/UDP 路由由 stream edge 在指定公网端口动态监听。TCP 进行字节转发；UDP 按来源地址建立会话，并把数据报封装到隧道内 TCP 连接。

## Agent 与 Client

Agent 在 NAS 上注册后把长期身份持久化到 `/data/agent.json`，周期性拉取期望状态、探测本地目标、协调 OpenSSH `-R` 转发、发送心跳并上报进程资源。每个注册 Agent 在 API 中是一个 Client。

::: warning 连接隔离
`tunnel_group` 会被保存，但当前一个 Agent 只维护一个 OpenSSH ControlMaster。Web 与高流量媒体需要独立 SSH 主连接时，请运行两个 Agent/Client，并使用不同状态目录。
:::

## Route

所有路由都有名称、Client、本地目标、启用状态、期望/观测 revision 和自动分配的 VPS 回环端口。

- Web 路由：`http` 或 `https`，必须有域名；可选路径前缀、去前缀和自定义公网端口。
- Stream 路由：`tcp` 或 `udp`，不使用域名/路径，必须指定公网端口。
- 同一 Web 端点由 `(协议, 域名, 公网端口, 路径前缀)` 唯一标识；同一 TCP/UDP 公网端口只能属于一条 stream 路由。

## 期望、观测与发布

API 写入只改变期望状态。Agent 的 observed revision 必须与当前 desired revision **相等**、Tunnel 为 `up` 且心跳仍新鲜（90 秒窗口）后，路由才具备发布条件；落后表示尚未收敛，超前值会被 API 拒绝。Local、Tunnel、Public 分层展示，`published` 不等于公网 DNS、证书或防火墙一定正确。

## 指标

Server 在内存中累计请求/会话数、进出字节和近 60 分钟序列，并采样自身及 Agent 进程 CPU/RSS。当前删除路由不会立即清除已累计的 route ID 计数；Server 重启会清空全部指标。
