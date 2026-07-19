# 客户端与注册

## Add Agent 与注册令牌

在控制台打开 **Add Agent**，点击 **Generate install command**：

1. Agent 名称为 1–64 字符，首字符必须是字母或数字，只能使用字母、数字、`.`、`_`、`-`；
2. 公网 Server URL 使用 HTTPS，填写 Public Server host 与 SSH tunnel port；
3. WebUI 有效期选项为 1、6、24 小时（API 最大 30 天）；
4. 生成后立即复制完整命令。明文令牌只出现一次，关闭弹窗、退出或重新登录后不能找回；
5. 命令固定当前 Server 版本，并优先使用 `curl`、无 `curl` 时回退 `wget`。

令牌有效期最大 30 天、只能成功注册一次，Server 只保存校验值。令牌表会显示未使用、已使用或已过期状态。在 **Add Agent** 的令牌表中找到目标 ID，点击该行 **Delete** 并核对确认框：

- 删除未使用令牌后，尚未执行的命令立即失效；
- 删除已使用/过期记录只清理该令牌记录；
- 已注册 Agent 的长期凭据、SSH 公钥和运行状态不受影响；
- UI 没有独立的 revoked 状态；`DELETE /api/v1/enrollment-tokens/{id}` 成功后，该行直接消失。

## 首次启动与安全重试

Agent 先生成请求 ID 和长期 Agent Token，原子写入 `/data/agent.json.pending`，再连同一次性令牌提交注册。Server 只保存长期 Token 的校验值。`/data/agent.json` 持久化后，安装器才删除 pending Claim 和 `.env` 中的一次性令牌。

相同 Claim 可安全重试：即使注册响应丢失，Agent 也能用同一请求 ID 取回原 Client，而不会二次消费令牌。安装失败时修复错误后重跑同一条命令；不要删除状态目录或生成新 Agent 名称来“碰碰运气”。

v0.4 Agent 安装器会检查 Docker daemon、Compose v2，并兼容 `docker compose` 与独立 `docker-compose` v2、常见 NAS PATH、多种 SHA-256 工具，以及无 `flock` 时的目录锁。成功后会再次无令牌重启并等待心跳，确认长期身份可用。

## 备份与恢复

必须备份完整 Agent 安装目录 `~/.portloom/agent/`，而不是只备份 `data/`。恢复同一安装至少需要：

- `.env`、`compose.yml`：安装配置与固定的不可变镜像 ID；
- `data/agent.json`：Client ID 与长期凭据；
- `data/ssh/id_ed25519`：Agent SSH 私钥；
- `data/ssh/known_hosts`：固定的 Server 主机公钥。

例如创建权限受限的完整归档：

```bash
umask 077
tar -C "$HOME/.portloom" -czf "$HOME/portloom-agent-backup.tgz" agent
```

仅有 `data/` 而缺少 `.env`/`compose.yml` 时，安装器会 fail closed，不会将其当作可直接恢复的完整安装。

不要让两个 Agent 共享同一个状态目录。需要 Web 与媒体流量使用独立 SSH 主连接时，创建两个 Client、两个安装目录和两个注册令牌。
