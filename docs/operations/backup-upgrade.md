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

## 在 NAS 本地执行切换预检

切换 VPS 前，可在 NAS 上运行 `portloomctl plan`，对比两台 PortLoom Server 的路由清单。发布后可从 GitHub Release 下载对应的 v0.5.0 Linux 二进制并对照 `SHA256SUMS` 校验，也可从候选版源码构建：

```bash
go build -o ./portloomctl ./cmd/portloomctl
```

在 NAS 的私有目录中保存 `profiles.json`，并将示例 API 地址、管理域名和业务域名替换为实际值：

```json
{
  "profiles": {
    "old": {
      "url": "https://old-api.example.net",
      "management_host": "old-console.example.net",
      "token_file": "old.token"
    },
    "new": {
      "url": "https://new-api.example.net",
      "management_host": "new-console.example.net",
      "token_file": "new.token",
      "ca_file": "new-private-ca.pem"
    }
  },
  "groups": {
    "media": { "hosts": ["photos.example.net", "video.example.net"] }
  }
}
```

将两台 Server 的管理员 Bearer Token 分别存入独立的普通文件，禁止同组和其他用户访问（例如 `chmod 600 old.token new.token`）。不要将 Token 放入 JSON、URL、命令行参数或版本库。文件路径可用绝对路径，也可相对于 `profiles.json`。两个 API 必须是不同的 HTTPS 源地址，不得包含凭据、路径前缀、查询参数或片段。若使用私有 CA，通过 `ca_file` 指定可信的 PEM 文件，否则省略该字段。CLI 会验证 API 的 TLS，直连而不使用环境代理，也不会跟随重定向。

`groups` 可省略。映射组是一组有名称、非空的精确业务 DNS 主机名（不允许通配符、IP、URL、端口或路径）。组名长度为 1–64 个 ASCII 字符，以英文字母或数字开头，后续可用英文字母、数字、`.`、`_` 或 `-`。选择组时必须使用定义中的精确拼写，不可定义仅大小写不同的组名。所有组都会在访问 API 前校验；主机名会规范化、去重并排序。同一映射组可用于不同的 Server 组合，每次都必须明确指定配置名和已注册客户端的 **ID**。请用对应 Server 的客户端清单中的 ID 替换 `source-client-id` 和 `target-client-id`。

```bash
./portloomctl plan --config ./profiles.json --source old --target new \
  --source-client source-client-id --target-client target-client-id --group media
```

一次性预检可将 `--group media` 换成多个 `--host`，两种选择方式不能混用：

```bash
./portloomctl plan --config ./profiles.json --source old --target new \
  --source-client source-client-id --target-client target-client-id \
  --host photos.example.net --host video.example.net
```

预检仅向 `/api/v1/clients` 和 `/api/v1/routes` 发送管理员清单 `GET` 请求。它以完整主机名为检查单位：`routes` 包含源端所有匹配记录，包括停用记录和同一主机名下的不同路径。支持范围仅限公网 443 端口的 HTTPS 路由。请查看 `blockers`，确认是否缺少客户端或主机名、选中管理或 API 主机名、主机名由其他源端客户端占用、端点不受支持、本地目标无效，或目标端已存在该主机名的 Web 路由（即使已停用或路径不同）。

| 退出码 | 含义 |
| --- | --- |
| `0` | JSON 中 `status: "preview"`：清单中未发现阻塞项。 |
| `1` | JSON 中 `status: "blocked"`：请查看 `blockers`；如果清单读取失败，则仅在 stderr 输出错误。 |
| `2` | 调用参数或本地配置无效，不输出清单报告。 |

请核对 JSON 中的 `group`（仅在使用 `--group` 时出现）、展开后的 `hosts` 和 `routes` 是否符合预期。若报告包含内部地址，请勿公开。**预览不代表切换已就绪，也不是切换授权。** 清单读取不是原子快照，组名和报告也不会通过哈希或 CAS（比较并交换）锁定清单。`checks` 将 SSH、业务 TLS、公网可达性和 DNS 标记为 `not_performed`；变更前需另行验证。`plan` 始终返回 `execute: "unavailable"`，没有 `apply` 命令；预检不会修改路由、Agent、服务或 DNS。Linux 的 `switch` 使用独立的操作配置，不能直接执行 `plan`。前提与限制见 [v0.5.0 发布说明](./release-v0.5.0.md)。

## v0.4.6 → v0.5.0 Server 升级

现有 v0.4.6 安装器托管栈应在维护窗口内升级。备份前暂停业务流量和管理写入，验证升级成功后再恢复。不要让两个 Server 进程共用同一个数据库。v0.5.0 制品发布前，下列命令仅供候选版验收。首次成功变更发布闸门后，不得用 v0.4.6 恢复，请参见下文的降级保护说明。

将安装器下载到安装目录之外的工作目录：

```bash
curl -fsSLo install-server.sh https://docs.look4i.com/install-server.sh && chmod 0700 install-server.sh
```

在同一 shell 中，根据原 `.env` 设置并导出 `SERVER_HOME`（原安装目录的绝对路径）、`SERVER_DOMAIN` 和下列五个端口变量。默认安装目录为 `$HOME/.portloom/server`，Web、SSH、Gateway、HTTP、HTTPS 端口依次为 `8080`、`2222`、`8081`、`80`、`443`。非默认安装使用原值，不要用 `source` 加载 `.env`。请在下载目录执行以下命令；需要有 Docker 操作权限及使用 `sudo` 归档容器所有文件的权限：

```bash
(
  set -eu
  umask 077
  : "${SERVER_HOME:?Set the original absolute install path}" \
    "${SERVER_DOMAIN:?Set the original domain}" "${SERVER_WEB_PORT:?Set the original Web port}" \
    "${SERVER_SSH_PORT:?Set the original SSH port}" "${SERVER_GATEWAY_PORT:?Set the original Gateway port}" \
    "${SERVER_HTTP_PORT:?Set the original HTTP port}" "${SERVER_HTTPS_PORT:?Set the original HTTPS port}"
  case "$SERVER_HOME" in /*) ;; *) echo 'SERVER_HOME must be absolute' >&2; exit 1;; esac
  installer="$PWD/install-server.sh"
  test -x "$installer"
  cd "$SERVER_HOME"
  test -f .env && test -f compose.yml || exit 1
  compose() { docker compose --env-file .env -f compose.yml "$@"; }
  sudo -v
  backup_dir=$(mktemp -d "$HOME/portloom-server-v0.4.6.XXXXXXXX")
  for service in server sshd; do
    container=$(compose ps -q "$service")
    test -n "$container"
    docker inspect --format '{{.Name}} {{.Image}}' "$container"
  done > "$backup_dir/images.txt"
  compose stop server sshd
  sudo tar -cpf "$backup_dir/server-install.tar" -- .env compose.yml server-data ssh-hostkeys ssh-auth
  sudo tar -df "$backup_dir/server-install.tar" >/dev/null
  printf 'Backup: %s\n' "$backup_dir"
  compose start server sshd
  PORTLOOM_GATEWAY_PORT="$SERVER_GATEWAY_PORT" "$installer" \
    --domain "$SERVER_DOMAIN" --home "$SERVER_HOME" --web-port "$SERVER_WEB_PORT" \
    --ssh-port "$SERVER_SSH_PORT" --http-port "$SERVER_HTTP_PORT" \
    --https-port "$SERVER_HTTPS_PORT" --version 0.5.0
)
```

`stop` 停止服务但保留旧容器，以便离线备份。`start` 恢复同一组旧容器，不重建容器也不拉取镜像。这一步不可省略：即使不可变镜像 ID 匹配，安装器也会拒绝已停止的容器。由安装器替换正在运行的旧容器；不要运行 `down`，也不要启动另一套 Server。备份或校验失败时，命令会在 `start` 和升级前中止；修复备份问题再继续。请妥善保护包含凭据和 SSH 密钥的归档，并保留 `images.txt` 记录的旧镜像以供恢复。

安装器会解析并持久化不可变镜像 ID、生成候选配置、创建 `native-upgrade-backup-0.5.0/` 配置备份、用 Compose `up -d` 更新，并实际访问 HTTPS `/healthz`；启动验证失败时会尝试恢复旧配置并用旧镜像 ID 重启；不会恢复 Server 数据。同名备份目录已存在会拒绝继续。

从不含 stream-edge 值的 v0.3 安装首次迁移时，可追加 `--disable-tcp-edge` 写入 `off`。已有 `.env` 的非空值会被安装器保留，后续重跑参数不是通用开关；如需变更，应先备份并审查安装目录中的 `.env`/Compose。启用 stream edge 时只放行实际路由端口，不要开放整个范围。

升级后检查：

```bash
(
  set -eu
  cd "${SERVER_HOME:?Set the original absolute install path}"
  docker compose --env-file .env -f compose.yml ps
  docker compose --env-file .env -f compose.yml logs --tail=200 server
  curl -fsS "https://${SERVER_DOMAIN:?Set the original domain}:${SERVER_HTTPS_PORT:?Set the original HTTPS port}/healthz"
)
```

确认旧 Web 路由在数据库迁移后显示为 HTTPS、`/api/v1/system` 为 0.5.0、Dashboard 指标出现，并验证真正的明文 HTTP、HTTPS、TCP、UDP、路径与额外端口。

## 从 v0.2.x Caddy 安装迁移

```bash
./install-server.sh \
  --domain example.com \
  --version 0.5.0 \
  --migrate-native-edge
```

必须使用原域名与全部原端口，并提前备份 Caddy 卷、完整 Server 数据和旧 Agent 状态，同时记录旧 Server/sshd/Caddy 容器的精确镜像 ID。安装器会保存旧 `.env`、`compose.yml`、`Caddyfile`，停止旧 Caddy，启动原生 edge 并验证 HTTPS；该自动配置备份不包含数据库或旧镜像。公网 80 必须到达配置的 HTTP edge。

## 回滚

任何回滚先保留一份故障现场副本，再停止当前 Compose。不要清空 `server-data`、`ssh-hostkeys`、`ssh-auth` 或 Agent 数据。

### 已使用发布闸门的 Server 数据库

不要降级已经使用过发布闸门的数据库。首次成功变更闸门状态时会写入持久的 SQLite 降级保护；即使释放全部闸门，保护也不会移除。v0.4.6 Server 连接到该数据库时，会在开启监听器前退出，并记录 `PortLoom: publication-gated database; restart with a gate-aware Server`。保护不会停止已经运行的旧进程，因此升级前必须停止旧 Server，且不能让多个 Server 进程共用数据库。

如果降级被拒绝，停止尝试启动的旧栈及其托管 SSH 服务。保留完整 Server 数据（包括可能存在的 SQLite 附属文件），不要变更切换 DNS、对端发布闸门或 Agent 分配。使用上次正常运行且**包含此降级保护**的 Server 不可变镜像与配套配置，连接原有且未经修改的数据目录。重新开放入口前，核对两端的闸门阶段和代数，并确认受阻断的主机仍无法通过恢复节点访问。如果无法确认镜像或发布归属，保持受影响的入口及托管 SSH 隔离。禁止删除触发器、闸门表或闸门记录，也不要让 v0.4.6 连接带闸门的数据库副本。闸门启用前的备份可能包含后来已迁移到对端的路由；恢复该备份并不代表可以安全地重新对外发布这些路由。

- **v0.4 → v0.3.x：**恢复 `native-upgrade-backup-0.4.6/` 中 `.env`、`compose.yml`，同时恢复升级前一致的 `server-data/`。把记录的不可变 ID 同时传给 v0.3 使用的 `PORTLOOM_*_IMAGE` 与 v0.4 使用的 `PORTLOOM_*_IMAGE_ID`，防止本地移动 Tag 被误用：

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
