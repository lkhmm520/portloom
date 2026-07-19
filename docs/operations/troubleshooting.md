# 故障排查

| 现象 | 优先检查 |
| --- | --- |
| 控制台拒绝 Token | `TM_ADMIN_TOKEN`、Bearer Header、是否访问正确 HTTPS 管理域名 |
| Agent 安装找不到 Docker | Synology Container Manager / QNAP Container Station、PATH、当前用户权限 |
| Compose v2 报错 | `docker compose version` 或 `docker-compose version --short` 必须为 v2 |
| Agent 注册失败 | 令牌是否被删除/过期/已用、系统时间、证书链、Server URL 无路径 |
| Agent 重跑身份不匹配 | 必须用原名称/Server/SSH 主机/端口/`--home`，不要覆盖状态 |
| Local down | 本地目标监听、地址/端口、容器网络、NAS 防火墙 |
| UDP Local up 但无数据 | Local 只表示 UDP relay 已创建；检查真实 UDP 服务、响应与端到端数据报 |
| Tunnel down | 私钥、known_hosts、受管 SSH、回环端口冲突、2222 出站 |
| Public `waiting_agent` | revision、Tunnel、最近心跳（90 秒窗口） |
| Public `conflict` | 同一 extra Web 端口混用 HTTP/HTTPS，或 TCP/UDP 公网端口重复 |
| Public `bind_error` | 端口已被进程占用、bind IP 不存在、低端口 capability/权限 |
| HTTP/HTTPS 404 | scheme、端口、Host、路径前缀、路由启用/收敛状态 |
| 502 | 路由已匹配，但 SSH 回环上游不可用 |
| 自定义 HTTPS 能连但证书失败 | 公网 80 是否仍到达主 HTTP edge，DNS 是否指向该 Server |
| Dashboard 无指标 | `/api/v1/metrics`、是否已有请求/会话；重启后计数归零 |

## 常用命令

```bash
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml ps
docker compose --env-file .env -f compose.yml logs --tail=200 server
curl -sS http://127.0.0.1:8080/healthz
curl -i -H 'Host: app.example.com' http://127.0.0.1:8081/
```

确认端口是否被占用时检查主 edge、2222、路由公网端口和 20000–29999 回环池。不要通过删除 SQLite、Agent identity、私钥或整个安装目录来排障；先备份，再按 Local → Tunnel/revision → PortLoom edge → DNS/ACME/防火墙顺序缩小范围。

## 安装器失败后的处理

Agent 安装器会打印失败步骤和中英文建议。修复 Docker daemon、Compose 或网络后重跑同一命令即可；无 `flock` 环境使用 `.install.lock.d`，只有确认没有安装进程且上次被强制中断时才删除该锁目录。

Server 安装器要求 `flock`；QNAP/Entware 可安装 `/opt/bin/opkg install flock`。升级失败先查看安装器是否已自动恢复，不要手动切换可变 `latest`。
