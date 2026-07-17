# 备份、升级与回滚

## 备份范围

Server 简易安装目录默认为 `~/.portloom/server`。备份至少应包含：

- `server-data/`：SQLite 数据库、WAL/SHM，以及 `certs/` 中的 ACME 证书缓存；
- `ssh-hostkeys/`：Agent 已固定信任的 Server SSH 身份；
- `ssh-auth/`：当前生成的 Agent 授权文件；
- `.env` 与 `compose.yml`（包含敏感信息，备份权限应为 0600/0700）；
- 从 v0.2.x 迁移时，还要保留 `Caddyfile`、`caddy-data/` 和 `caddy-config/`；
- Agent 的 `/data/agent.json`、SSH 私钥及已核验的 `known_hosts`。

零停机备份 SQLite 时使用在线备份 API。直接复制 `server-data/` 前应停止 Server，或一致性保存数据库、WAL 与 SHM。不要只复制 `portloom.db`，也不要遗漏 `server-data/certs/` 或 `ssh-hostkeys/`。

## 常规升级

手动维护的 Compose 部署可以固定新镜像标签后逐项升级：

```bash
docker compose pull
docker compose up -d --pull never
docker compose ps
docker compose logs --tail=100
```

一次只升级一个组件。先 Server，再普通 Web Agent，最后高流量媒体 Agent；每步确认心跳、revision、HTTPS 和公网请求。

### v0.3 原生入口简易安装升级

对已使用 v0.3 原生入口的简易安装，使用原域名和端口重新运行安装器，并传入与当前镜像引用不同的固定目标版本：

```bash
./install-server.sh --domain portloom.example.com --version 0.3.1
```

为避免 `latest` 等可变标签在本地改指后破坏重跑和回滚，安装器会持久化 Server/sshd 的不可变镜像 ID，Compose 也只使用这些 ID；镜像引用未变化时不会再次 pull。升级必须传入新的固定版本。镜像标签变化时，安装器会先完整生成候选文件，再创建 `native-upgrade-backup-0.3.1/`（目录后缀是目标版本），其中保存升级前的 `.env` 与 `compose.yml`。新版本只有通过实际 HTTPS `/healthz` 验证才算成功；失败时安装器会用旧不可变 ID 恢复旧 Compose、旧配置并验证旧 HTTPS。若自动恢复无法验证，必须按报错人工检查。成功后保留该备份目录，确认稳定后可归档或删除。

## 从 v0.2.x Caddy 简易安装迁移到 v0.3.0 原生入口

旧简易安装包含 Caddy，仅执行 `docker compose pull/up` 不会替换旧的生成文件，也不会释放 80/443。请先完整备份安装目录，然后显式运行迁移：

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
chmod 0700 install-server.sh
./install-server.sh \
  --domain portloom.example.com \
  --version 0.3.0 \
  --migrate-native-edge
```

迁移必须使用原安装的域名、Web/SSH/Gateway 端口以及原 Caddy 的 80/443 端口，并传入与旧 Server 镜像引用不同的固定 v0.3 `--version`；安装器拒绝用相同的 `latest` 引用覆盖旧部署，以保证回滚仍指向旧镜像。如果旧 Caddy 使用自定义本地入口端口，请在命令前设置对应的 `PORTLOOM_HTTP_PORT` 与 `PORTLOOM_HTTPS_PORT`；公网 80/443 必须通过 NAT/端口转发到这些本地端口，HTTP 跳转会包含自定义 HTTPS 端口。安装器会：

1. 拒绝未显式提供 `--migrate-native-edge` 的旧 Caddy 安装；
2. 先完整生成候选配置，再原子建立权限为 0700 的 `migration-backup-v0.3.0/` 目录，其中保存原 `.env`、`compose.yml` 和 `Caddyfile`；
3. 生成仅含 `portloom-server` 与 `portloom-sshd` 的新配置；
4. 在启动原生入口前停止旧 Caddy，再以 `--remove-orphans` 启动新配置；
5. 等待 Server 健康，并通过本机回环实际访问管理域名 HTTPS；只有证书签发、443 监听和 `/healthz` 均成功才报告完成。失败时停止新配置、恢复旧 Compose 与三个标准文件并验证旧 HTTPS；若自动恢复无法验证，安装器会明确报错，必须人工检查服务状态。

迁移后验证：

```bash
cd ~/.portloom/server
docker compose ps
docker compose logs --tail=100 server
curl -I http://portloom.example.com
curl -I https://portloom.example.com
```

确认 `portloom-caddy` 已不存在，HTTP 跳转到 HTTPS，WebUI 证书有效，并新建一个 HTTP 路由验证按域名签发和转发。

## 回滚 v0.3.0 原生入口迁移

自动恢复失败或需要手动回滚时，先停止新配置，再恢复安装器生成的三个备份文件：

```bash
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml down --remove-orphans
cp migration-backup-v0.3.0/.env .env
cp migration-backup-v0.3.0/compose.yml compose.yml
cp migration-backup-v0.3.0/Caddyfile Caddyfile
docker compose --env-file .env -f compose.yml up -d --pull never
docker compose ps
```

保留 `server-data/`、`ssh-hostkeys/`、`ssh-auth/`、`caddy-data/` 和 `caddy-config/`；不要在回滚时清空这些目录。检查旧 Caddy 恢复 80/443 后，再验证 WebUI 和现有路由。

## 普通版本回滚

固定旧镜像标签并使用已备份的 Compose 配合 `up -d --pull never` 重建；如果旧镜像在本机不存在，应停止并人工恢复正确镜像，不能让 Compose 隐式拉取可变标签。数据库变更前必须先备份；入口回滚还必须恢复原公网入口和上游映射，不能只启动旧隧道容器。
