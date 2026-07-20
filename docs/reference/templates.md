# 模板下载

以下文件来自公开白名单，每次构建仅复制列出的模板；真实 `.env`、密钥和运行数据不会进入文档站。

<DownloadCard title="新手 Server compose.yml" description="推荐：复制 .env 模板并修改两项即可启动" file="compose.yml" />
<DownloadCard title="新手 Server .env 模板" description="下载后重命名为 .env" file="compose.env.example" />
<DownloadCard title="Server Compose" description="使用 GHCR Server 镜像" file="docker-compose.server.yml" />
<DownloadCard title="Server 环境变量" description="复制为 server.env 后填写" file="server.env.example" />
<DownloadCard title="高级：单 Agent Compose" description="手动恢复/专家部署；新安装请使用 WebUI 生成命令" file="docker-compose.agent.yml" />
<DownloadCard title="高级：Agent 环境变量" description="手动恢复时复制为 agent.env" file="agent.env.example" />
<DownloadCard title="高级：双 Agent Compose" description="Web 与媒体独立 SSH 主连接" file="docker-compose.dual-agent.yml" />
<DownloadCard title="Web Agent 环境变量" description="复制为 agent-web.env" file="agent-web.env.example" />
<DownloadCard title="媒体 Agent 环境变量" description="复制为 agent-media.env" file="agent-media.env.example" />
<DownloadCard title="sshd 限制片段" description="禁用 Shell，仅保留回环远程转发" file="sshd_config.portloom.conf" />

::: warning
新手优先使用[Compose 模板安装](/guide/compose-install)中的 `compose.yml` 与 `compose.env.example`。下方命令针对高级拆分模板。

新 Agent 必须优先从 WebUI **Add Agent** 复制专用命令；不要手填一次性 Token，也不要用 `ssh-keyscan` 临时建立信任。生成命令会固定该 Server 的 SSH 主机公钥，并在注册成功后移除一次性 Token。Agent 模板仅用于专家部署或持有完整身份备份时的手动恢复。

模板中的 `change-me` 必须替换。Compose 的服务级 `env_file` 不参与 YAML 插值，因此必须显式传入对应环境文件：

```bash
cp server.env.example server.env
docker compose --env-file server.env -f docker-compose.server.yml config

cp agent.env.example agent.env
docker compose --env-file agent.env -f docker-compose.agent.yml config

cp agent-web.env.example agent-web.env
cp agent-media.env.example agent-media.env
docker compose --env-file agent-web.env --env-file agent-media.env \
  -f docker-compose.dual-agent.yml config
```

检查渲染结果，但不要把包含真实 Token 的输出提交到 Git。

Server 模板为兼容非 root 绑定 80/443，先 `cap_drop: ALL`、再仅回加 `NET_BIND_SERVICE`，并特意不对 Server 设置 `no-new-privileges`，否则 Linux 会抑制二进制的 `cap_net_bind_service` file capability。不要在不了解该边界时重新添加 NNP；上线后同时验证容器 PID 1 非 root、`CapEff` 仅含 `0x400`，以及真实 80/443 请求。

请把模板放进专用安装目录，并将该目录设为 `0711`（只允许遍历，不允许其他用户列目录）；不要对整个家目录这样操作。模板中的一次性 `state-init` 会在空 bind mount 中创建 `data/server/certs`，把 Server 数据与授权文件交给 UID/GID 65532，并以 `0700`/`0600` 收紧权限。删除该初始化步骤或让上级目录不可遍历，会导致非 root Server 无法创建 SQLite 数据库，尤其常见于 NAS FUSE 路径。
:::
