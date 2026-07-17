# 备份、升级与回滚

## 备份范围

- Server SQLite 数据库及 WAL/SHM；
- Agent `/data/agent.json`；
- SSH 私钥与已核验 `known_hosts`；
- 不含明文密钥的 Compose 与环境模板；
- 现有公网入口的上游映射（如有）。

零停机备份 SQLite 时使用在线备份 API；直接复制文件前应停止 Server，或同时一致性保存数据库、WAL 与 SHM。

## 升级

```bash
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=100
```

一次只升级一个组件。先 Server，再普通 Web Agent，最后高流量媒体 Agent；每步确认心跳、revision 与公网请求。

## 回滚

固定旧镜像标签并保留旧 Compose：

```bash
PORTLOOM_SERVER_IMAGE=ghcr.io/lkhmm520/portloom-server:0.1.0 docker compose up -d
```

数据库变更前必须先备份。入口迁移的完整回滚还包括恢复原公网入口上游，不能只启动旧隧道容器。
