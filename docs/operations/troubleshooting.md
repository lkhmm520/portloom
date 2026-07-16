# 故障排查

| 现象 | 优先检查 |
| --- | --- |
| 控制台拒绝 Token | `TM_ADMIN_TOKEN`、Bearer Header、HTTPS代理 |
| Agent 无法注册 | 令牌是否过期/已使用、系统时间、证书链 |
| Agent 注册后反复失败 | `/data/agent.json`权限与持久化 |
| Local down | NAS目标监听、地址、端口、防火墙 |
| Tunnel down | 私钥可读性、known_hosts、SSH用户策略、端口冲突 |
| Gateway 404 | Host、路由协议、启用状态、域名规范化 |
| Gateway 502 | VPS分配端口无监听、SSH转发断开 |
| Revision pending | Agent心跳、期望/观测版本、最近错误 |

## 常用命令

```bash
docker compose ps
docker compose logs --tail=200 server
docker compose logs --tail=200 agent
curl -sS http://127.0.0.1:8080/healthz
curl -i -H 'Host: app.example.com' http://127.0.0.1:8081/
ssh -vvv -o ExitOnForwardFailure=yes tunnel@tunnel.example.com
```

不要把删除 SQLite、Agent 状态或重建数据库当作第一步。先备份，再按 Local → Tunnel → Gateway → NPM 的顺序定位。
