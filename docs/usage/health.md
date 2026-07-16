# 健康状态

PortLoom 故意不把所有状态压成一个“绿色圆点”。

## Local

Agent 能否在超时时间内连接 `local_host:local_port`。Local down 表示目标服务、地址、端口或 NAS 防火墙有问题。

## Tunnel

OpenSSH ControlMaster 是否有效，远程 `-R` 转发是否已创建。Tunnel down 常见于密钥权限、主机指纹、SSH账户策略或远程端口冲突。

## Public / Revision

`desired_revision` 与 `observed_revision` 一致，且路由已启用。它表示 Agent 已应用当前期望状态，不等于外部 DNS/TLS 一定正确。

## 正确的排查顺序

1. NAS 本地 `curl` 或 TCP 连接；
2. Agent 日志与 Local；
3. VPS 回环端口和 Tunnel；
4. Gateway 带 Host 请求；
5. NPM、TLS、DNS 与公网请求。
