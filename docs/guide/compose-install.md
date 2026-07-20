# 用 Compose 模板安装

这条路径适合希望**看得见配置、以后继续用 Compose 管理**的用户。你不需要先运行 PortLoom 安装脚本：下载两个文件、修改两项、启动项目即可。

::: tip 先说清楚域名
`TM_PUBLIC_HOST` 填你自己选定的**完整管理域名**。它可以是根域名（如 `example.com`），也可以是任意子域名（如 `tunnel.example.com`），**不要求 `portloom.` 前缀**。下面的 `example.com` 只是文档示例，不是固定值。
:::

## 你会安装到哪里

```text
访问者
  ↓ 你的管理域名 / 业务域名
公网 VPS：PortLoom Server + WebUI + 受管 sshd
  ↑ NAS 主动建立安全隧道（NAS 不开放入站端口）
内网 NAS：PortLoom Agent → 访问本地服务
```

| 主机 | 安装内容 | 要求 |
| --- | --- | --- |
| 公网 VPS | 本页的 Server Compose 项目 | Docker Compose v2；公网 IPv4；TCP 80/443/2222 |
| NAS / 内网服务器 | WebUI 生成的 Agent 安装项目 | Docker；可出站访问 VPS 的 HTTPS 与 SSH |

一个 Server 可以管理多个 Agent。先在 VPS 启动 Server，再从 WebUI 给每台 NAS 生成各自的 Agent 配置。

## 1. 准备域名和端口

1. 选一个你自己的管理域名，例如根域名 `example.com` 或任意子域名 `tunnel.example.com`。
2. 为它添加 A 记录，指向 VPS 的公网 IPv4。
3. VPS 放行 TCP `80`、`443`、`2222`，并确认本机 `80/443` 没有被其他程序占用。
4. NAS 不需要公网 IP，也不需要开放任何入站端口。

以后发布 `jellyfin.example.com` 等业务域名时，可以配置 `*.example.com` 通配符解析，也可以逐个添加 A 记录。

根域名可以使用；如果根域名已经承载网站，建议另选一个名称自定义的子域名避免冲突。这个子域名叫什么由你决定。

::: warning VPS 已经使用 80/443？
如果 VPS 上已有 Caddy、Nginx 或 Nginx Proxy Manager，不要直接启动默认模板。先看[生产环境部署](/install/production)和[反向代理接入](/install/reverse-proxy)。
:::

## 2. 下载两个文件

在 VPS 创建一个专用目录，例如 `portloom/`，然后把下面两个文件放在同一目录：

<DownloadCard title="下载 compose.yml" description="Server、受管 sshd 与初始化服务" file="compose.yml" />
<DownloadCard title="下载环境变量模板" description="下载 compose.env.example 后重命名为 .env" file="compose.env.example" />

把 `compose.env.example` 重命名为 `.env`。最终目录只需这样：

```text
portloom/
├── compose.yml
└── .env
```

你可以直接用浏览器下载后，通过 NAS/VPS 文件管理器上传和重命名；不要求用 `curl`。

## 3. 只修改两个必填值

打开 `.env`：

```dotenv
TM_PUBLIC_HOST=
TM_ADMIN_TOKEN=
```

- `TM_PUBLIC_HOST`：填写第 1 步选定的完整 DNS 主机名，例如 `tunnel.example.com`。不要包含 `https://`、端口或路径。
- `TM_ADMIN_TOKEN`：在终端运行 `openssl rand -hex 32`，把输出的 64 位十六进制字符串粘贴到等号后；它是 WebUI 登录凭证。也可用密码管理器生成只含 `0-9`、`a-f` 的 64 位值。不要使用 `$`、`#`、引号或空格，避免 dotenv/Compose 重新解释。

两个值留空时 Compose 会拒绝启动，避免带着公开占位凭证误上线。

其他配置可以先保持默认。`TM_TCP_EDGE_BIND_HOST=0.0.0.0` 允许以后发布 TCP/UDP；如果只使用网页路由，可改为 `off`。

::: danger 不要公开 `.env`
`.env` 包含管理员令牌，不要提交到 Git、截图发到 Issue 或转发给别人。
:::

## 4. 启动 Compose 项目

### 使用 NAS / 服务器的 Compose 图形界面

1. 新建一个 Compose/Project 项目；
2. 选择包含 `compose.yml` 和 `.env` 的目录；
3. 把专用项目目录权限设为 `0711`（可遍历、不可列目录），并把 `.env` 设为仅所有者可读写（`0600`）；不要把整个家目录改成 `0711`；
4. 先执行“验证/解析”，确认没有缺少变量；
5. 点击“创建”或“启动”。

### 使用终端

在这两个文件所在目录执行：

```bash
chmod 0711 .
chmod 0600 .env
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose ps -a
```

正常情况下会看到：

- `portloom-server`：运行中；
- `portloom-sshd`：运行中且健康；
- `state-init`：成功退出（它只负责首次初始化权限，不是故障）。

## 5. 打开 WebUI

访问：

```text
https://你在 TM_PUBLIC_HOST 填写的域名
```

输入 `.env` 中的 `TM_ADMIN_TOKEN`。第一次访问可能需要几十秒完成证书申请；如果打不开，先检查 DNS、VPS 的 80/443 防火墙和端口占用，再查看：

```bash
docker compose logs --tail=100 server sshd
```

页面可以打开后，再验证健康端点：

```bash
curl --fail --head https://你的管理域名/healthz
```

## 6. 添加 NAS Agent

进入 **Add Agent**，填写 Agent 名称、Server URL、公网 SSH 主机和端口 `2222`，点击 **Generate command**，再把生成的完整命令粘贴到 NAS。

Agent 这里保留生成命令，是为了自动完成一次性注册令牌、Ed25519 密钥、Server SSH 主机公钥固定和注册后清理 Token；手写 Agent Compose 很容易漏掉这些安全步骤。Server 的安装与日常管理仍完全使用本页的 `compose.yml`。

Agent 出现在 WebUI 后，按[五分钟快速开始](/guide/quick-start#_4-添加第一条-https-路由)创建第一条路由。

## 数据、重启和升级

- 模板已固定经过验证的 Server/sshd `0.4.1` 镜像和项目内 `./data/` 路径，避免 Shell 环境变量静默改变镜像或数据源。高级定制应直接审查并修改 `compose.yml`。
- 专用项目目录保持 `0711`，让 NAS/FUSE 路径上的非 root 容器可以遍历；私密 `.env` 保持 `0600`。不要把这些权限套到整个家目录。
- 日常只重启长驻服务：`docker compose restart server sshd`。配置或镜像变化使用 `docker compose up -d`，不要单独重启一次性 `state-init`。
- 不要用临时 `docker run`、不要删除数据目录后重建。
- 升级前停止 `server` 和 `sshd`，备份**整个项目目录**（`compose.yml`、私密 `.env`、完整 `data/`），并记录 `docker compose images` 输出；备份后再 `docker compose up -d` 恢复服务。不要只复制运行中 SQLite 的 `portloom.db`。完整步骤见[备份、升级与回滚](/operations/backup-upgrade)。

自动生成随机凭证、固定不可变镜像并提供失败自动回滚的路径仍然保留，见[安装脚本方式](/install/docker#方式二-安全安装脚本)。完整模板和高级 Agent 示例见[模板下载](/reference/templates)。
