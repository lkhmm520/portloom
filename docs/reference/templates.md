# 模板下载

以下文件来自公开白名单，每次构建仅复制列出的模板；真实 `.env`、密钥和运行数据不会进入文档站。

<DownloadCard title="Server Compose" description="使用 GHCR Server 镜像" file="docker-compose.server.yml" />
<DownloadCard title="Server 环境变量" description="复制为 server.env 后填写" file="server.env.example" />
<DownloadCard title="单 Agent Compose" description="普通 NAS 部署" file="docker-compose.agent.yml" />
<DownloadCard title="Agent 环境变量" description="复制为 agent.env 后填写" file="agent.env.example" />
<DownloadCard title="双 Agent Compose" description="Web 与媒体独立 SSH 主连接" file="docker-compose.dual-agent.yml" />
<DownloadCard title="Web Agent 环境变量" description="复制为 agent-web.env" file="agent-web.env.example" />
<DownloadCard title="媒体 Agent 环境变量" description="复制为 agent-media.env" file="agent-media.env.example" />
<DownloadCard title="sshd 限制片段" description="禁用 Shell，仅保留回环远程转发" file="sshd_config.portloom.conf" />

::: warning
模板中的 `change-me` 必须替换。执行 `docker compose config` 检查渲染结果，但不要把包含真实 Token 的输出提交到 Git。
:::
