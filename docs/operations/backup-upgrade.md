# 备份、升级与回滚

## 备份范围

Server 简易安装默认位于 `~/.portloom/server`。升级前至少保存：

- `server-data/`：SQLite、WAL/SHM 与 `certs/`；
- `ssh-hostkeys/`：Agent 已固定信任的 SSH 身份；
- `ssh-auth/`、`.env`、`compose.yml`；
- 每个 Agent 的**完整安装目录**，默认是 `~/.portloom/agent/`，必须同时包含 `data/`、`.env`、`compose.yml` 与其中固定的 `PORTLOOM_AGENT_IMAGE_ID`；
- 从 v0.2 Caddy 安装迁移时的 `Caddyfile`、`caddy-data/`、`caddy-config/`，以及旧 Compose 中每个运行容器的精确镜像 ID。

仅保存 Agent `data/` 不能通过安装器恢复；凭据存在但缺少 `.env`/`compose.yml` 时，安装器会 fail closed。备份应保持原 0700/0600 权限，并在隔离目录检查这些文件齐全。直接复制 SQLite 时应停止 Server，或使用 SQLite 在线备份并确保主库/WAL/SHM 一致；不要只复制 `portloom.db`。

迁移前可记录当前容器实际使用的镜像 ID：

```bash
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml ps -q \
  | xargs -r docker inspect --format '{{.Name}} {{.Image}}'
```

::: danger v0.4 数据库与 Agent 兼容性
v0.4 首次启动会增加路由字段/索引，并把早期版本的 legacy `http` 行改成 `https`，以保持旧版自动 TLS 行为。v0.3.2 Agent 只接受 `http|tcp`，会拒绝迁移后的 `https` 路由；v0.3.2 Gateway 也不会发布这些 `https` 行。

安装器的 `native-upgrade-backup-*` 只保存 `.env` 与 `compose.yml`，**不会备份数据库**。回滚到 v0.3.x 必须同时恢复 v0.4 首次启动前的一致 `server-data/`，不能只换旧镜像。
:::

## v0.3.x → v0.4.x：需要维护窗口

当前 `install-agent.sh` 只支持同版本续装/恢复，已有安装目录传入不同 `--version` 会 fail closed；WebUI 编辑路由时也锁定 Client。因此 v0.4.x **没有安装器管理的一键、零停机 Agent 跨版本升级路径**。不要先升级 Server 再让旧 Agent 长时间运行，也不要删除 `agent.json`、私钥或盲改不可变镜像 ID。

当前保守流程会产生短暂中断。以下命令假定 Agent 使用默认 home；自定义 home 必须替换为真实完整路径：

1. 停止写入，完整备份 Server 和每个 Agent 安装目录，并记录所有路由字段、原端口和旧镜像 ID。
2. 在旧 Agent 安装目录执行 Compose `down`，移除固定名称的旧容器但保留 bind-mounted 数据：

   ```bash
   cd ~/.portloom/agent
   docker compose --env-file .env -f compose.yml down
   ```

3. 使用原域名、原 Server 安装目录和原端口升级 Server。
4. v0.4 Server 就绪后，在 **Add Agent** 创建新名称，用不同 `--home` 安装全新的 v0.4 Agent。旧容器必须已 `down`，否则固定的 `container_name: portloom-agent` 会冲突。
5. WebUI 不能直接更改既有路由 Client。逐条记录并删除旧端点，再在新 Agent 上重建相同配置；等待 Local/Tunnel/Public 收敛并做真实端到端测试。
6. 保留升级前数据库、完整旧 Agent 目录和镜像 ID，直到回滚窗口结束。

需要无中断切换时，应等待项目提供经过测试的 Agent 跨版本事务，而不是自行猜测镜像 ID 切换流程。

## Server 升级命令

下载当前安装器，重复原参数并固定新版本：

```bash
curl -fsSLo install-server.sh https://docs.look4i.com/install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain example.com --version 0.4.1
```

非默认安装必须带回全部原配置；Gateway 没有 CLI 参数，需使用环境变量：

```bash
PORTLOOM_GATEWAY_PORT=<原Gateway端口> \
./install-server.sh \
  --domain example.com \
  --home <原安装目录> \
  --web-port <原Web端口> \
  --ssh-port <原SSH端口> \
  --http-port <原HTTP-edge端口> \
  --https-port <原HTTPS-edge端口> \
  --version 0.4.1
```

安装器会解析并持久化不可变镜像 ID、生成候选配置、创建 `native-upgrade-backup-0.4.1/` 配置备份、用 Compose `up -d` 更新，并实际访问 HTTPS `/healthz`；失败时恢复旧配置和旧镜像 ID。同名备份目录已存在会拒绝继续。

从不含 stream-edge 值的 v0.3 安装首次迁移时，可追加 `--disable-tcp-edge` 写入 `off`。已有 `.env` 的非空值会被安装器保留，后续重跑参数不是通用开关；如需变更，应先备份并审查安装目录中的 `.env`/Compose。启用 stream edge 时只放行实际路由端口，不要开放整个范围。

升级后检查：

```bash
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml ps
docker compose --env-file .env -f compose.yml logs --tail=200 server
curl -I https://example.com/healthz
```

确认旧 Web 路由在数据库迁移后显示为 HTTPS、`/api/v1/system` 为 0.4.1、Dashboard 指标出现，并验证真正的明文 HTTP、HTTPS、TCP、UDP、路径与额外端口。

## 从 v0.2.x Caddy 安装迁移

```bash
./install-server.sh \
  --domain example.com \
  --version 0.4.1 \
  --migrate-native-edge
```

必须使用原域名与全部原端口，并提前备份 Caddy 卷、完整 Server 数据和旧 Agent 状态，同时记录旧 Server/sshd/Caddy 容器的精确镜像 ID。安装器会保存旧 `.env`、`compose.yml`、`Caddyfile`，停止旧 Caddy，启动原生 edge 并验证 HTTPS；该自动配置备份不包含数据库或旧镜像。公网 80 必须到达配置的 HTTP edge。

## 回滚

任何回滚先保留一份故障现场副本，再停止当前 Compose。不要清空 `server-data`、`ssh-hostkeys`、`ssh-auth` 或 Agent 数据。

- **v0.4 → v0.3.x：**恢复 `native-upgrade-backup-0.4.1/` 中 `.env`、`compose.yml`，同时恢复升级前一致的 `server-data/`。把记录的不可变 ID 同时传给 v0.3 使用的 `PORTLOOM_*_IMAGE` 与 v0.4 使用的 `PORTLOOM_*_IMAGE_ID`，防止本地移动 Tag 被误用：

  ```bash
  OLD_SERVER_IMAGE_ID=sha256:replace-with-recorded-server-id
  OLD_SSHD_IMAGE_ID=sha256:replace-with-recorded-sshd-id
  for image_id in "$OLD_SERVER_IMAGE_ID" "$OLD_SSHD_IMAGE_ID"; do
    printf '%s\n' "$image_id" | grep -Eq '^sha256:[0-9a-f]{64}$' || {
      echo '旧镜像 ID 无效' >&2
      exit 1
    }
  done
  docker image inspect "$OLD_SERVER_IMAGE_ID" "$OLD_SSHD_IMAGE_ID" >/dev/null
  PORTLOOM_SERVER_IMAGE="$OLD_SERVER_IMAGE_ID" \
  PORTLOOM_SSHD_IMAGE="$OLD_SSHD_IMAGE_ID" \
  PORTLOOM_SERVER_IMAGE_ID="$OLD_SERVER_IMAGE_ID" \
  PORTLOOM_SSHD_IMAGE_ID="$OLD_SSHD_IMAGE_ID" \
    docker compose --env-file .env -f compose.yml up -d --pull never
  ```

  停止并 `down` 新 Agent，再从完整旧 Agent 目录以 `--pull never` 启动旧 Agent：

  ```bash
  OLD_AGENT_HOME=/absolute/path/to/complete-old-agent-install
  case "$OLD_AGENT_HOME" in /*) ;; *) echo 'OLD_AGENT_HOME 必须是绝对路径' >&2; exit 1;; esac
  test -f "$OLD_AGENT_HOME/.env" && test -f "$OLD_AGENT_HOME/compose.yml" || {
    echo 'OLD_AGENT_HOME 不是完整 Agent 安装目录' >&2
    exit 1
  }
  cd "$OLD_AGENT_HOME"
  docker compose --env-file .env -f compose.yml up -d --pull never
  ```

- **v0.2 Caddy 迁移回滚：**必须同时恢复 `migration-backup-v0.3.0/` 中 `.env`、`compose.yml`、`Caddyfile`，保留的 Caddy 卷，**以及迁移前一致的 `server-data/` 和旧 Agent 状态**。把旧 Compose 的每个镜像引用固定为迁移前记录的 `sha256:` ID，逐一用 `docker image inspect <sha256-ID>` 确认本地存在，再从原项目目录执行：

  ```bash
  docker compose --env-file .env -f compose.yml config
  docker compose --env-file .env -f compose.yml up -d --pull never
  ```

  任一旧镜像或迁移前数据库备份缺失都应立即中止，从可信归档恢复；绝不能拉取已移动的 `latest` 冒充旧栈。最后验证 80/443、WebUI、原有路由和旧 Agent 心跳。安装器的自动 edge 配置恢复不等于数据库回滚。
