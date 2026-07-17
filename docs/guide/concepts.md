# 核心概念

## Server 与 Gateway

同一 Server 进程始终包含两个内部监听器：管理监听器（默认 `127.0.0.1:8080`）提供控制台和 API；Gateway（默认 `127.0.0.1:8081`）按 HTTP `Host` 查找路由并代理到 SSH 回环端口。简易安装还启用原生公网 80/443 入口，由 Server 终止 HTTPS，并按 Host 把管理域名发送到管理处理器、把已启用的 HTTP 路由域名发送到 Gateway。

## Agent

Agent 在 NAS 上运行，注册一次后把客户端凭据持久化到 `/data/agent.json`。它周期性拉取期望状态、探测本地服务、协调 OpenSSH `-R` 转发并回报观测状态。

## Client

每个 Agent 注册为一个 Client。Client 有独立凭据与期望版本。若需要把大流量媒体与普通 Web 路由放在不同 SSH 主连接中，请运行两个 Agent/Client，而不是只填写不同 `tunnel_group`。

::: warning 当前限制
`tunnel_group` 字段会被保存，但当前 Agent 使用一个 OpenSSH ControlMaster。需要真正的连接隔离时，请使用双 Agent 模板。
:::

## Route

HTTP 路由包含域名、本地目标、启用状态和自动分配的 VPS 回环端口。域名唯一且会规范化。WebUI 只创建 HTTP 路由；已有 TCP 记录仅作为控制平面兼容元数据展示，不代表公网监听。

## 期望与观测状态

路由写入只表示期望状态已改变。只有 Agent 报告相同 revision，才表示配置已收敛。健康状态也分为本地可达、SSH 转发、公网收敛三层。
