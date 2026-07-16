# 系统架构

```text
Browser ──HTTPS──> NPM ──Host──> Gateway :8081
                    │                  │
                    │ admin           ▼
                    └────────> Server/SQLite
                                      ▲
                                      │ HTTPS heartbeat
NAS service <── Agent <── OpenSSH -R ─┘
```

## 控制平面

Server 的 SQLite 保存 Client、令牌校验值、Route、分配端口、revision 与 observation。当前设计预期单 Server 实例，不支持多实例同时写入。

## 数据平面

NPM 终止 TLS；Gateway 按 Host 查询已启用 HTTP 路由，代理到 VPS 的 `127.0.0.1:<allocated-port>`；宿主 OpenSSH 再把流量带回 NAS 本地服务。

## 故障行为

- Server短暂不可用时，已运行 Agent 保留已建立转发并重试；
- Agent重启后需要控制平面可用，才能恢复期望路由；
- SSH ControlMaster失效时，Agent重建主连接与转发；
- 取消转发失败时，Agent保留旧观测版本，不伪报收敛；
- 本地目标不可用时，隧道进程可能仍存在，但 Local 保持 down。

更深层设计说明见仓库原始 [`docs/architecture.md`](https://github.com/lkhmm520/portloom/blob/main/docs/architecture.md)。
