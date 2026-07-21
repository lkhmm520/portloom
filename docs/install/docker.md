# Docker 安装

PortLoom 分别安装在两台主机：公网 VPS 运行 Server、WebUI 与受管 sshd；NAS/内网主机运行 Agent 并主动连接公网。一个 Server 可以管理多个 Agent。

## 安装前准备

| 项目 | 公网 VPS | NAS / 内网主机 |
| --- | --- | --- |
| Docker | Docker Engine + Compose v2 | Docker；Agent 支持 Compose plugin 或独立 `docker-compose` v2 |
| 网络 | 公网 IPv4；默认开放 TCP 80/443/2222 | 只需出站访问 Server HTTPS 和 SSH |
| 域名 | 你自己选定的完整管理域名解析到 VPS | 不需要域名或公网 IP |

管理域名可以是根域名或任意子域名，**不要求 `portloom.` 前缀**。默认安装要求 VPS 本机 80/443 空闲。若已有 Caddy/Nginx/NPM，请先看[生产环境部署](/install/production)和[反向代理接入](/install/reverse-proxy)。

## 公网 VPS：选择一种 Server 安装方式

| 方式 | 适合谁 | 你要做什么 | 特点 |
| --- | --- | --- | --- |
| [Compose 模板](/guide/compose-install) | 习惯 Compose、NAS 项目界面或文件式配置 | 下载 `compose.yml` + `.env` 模板，修改两项后启动 | 配置直观，日常直接用 Compose 管理 |
| 安全安装脚本 | 希望自动生成随机凭证、固定镜像并验证回滚 | 下载并执行脚本 | 自动化更多，升级保护更完整 |

### 方式一：Compose 模板

专页已经给出从下载到 WebUI 登录的完整步骤：

- [用 Compose 模板安装](/guide/compose-install)
- [直接下载 `compose.yml`](/examples/compose.yml)
- `.env` 模板在 Compose 安装专页提供下载并说明两个必填值

模板默认直接提供公网 HTTPS、HTTP 与受管 SSH，不要求先安装反向代理。Server、sshd 和一次性初始化服务都包含在同一个 `compose.yml` 中。

### 方式二：安全安装脚本

```bash
curl -fsSLo install-server.sh https://docs.look4i.com/install-server.sh
less install-server.sh
chmod 0700 install-server.sh
DOMAIN='example.com' # 改成你选定的完整管理域名
./install-server.sh --domain "$DOMAIN" --version 0.4.1
```

安装器会生成：

```text
compose.yml       Server + sshd 的 Compose 配置
.env              固定镜像 ID、端口、管理域名、随机管理员令牌（0600）
server-data/      SQLite 与 certs/ 证书缓存
ssh-hostkeys/     Agent 固定信任的 Server SSH 身份
ssh-auth/         Server 从 SQLite 重建的 Agent 授权文件
```

Server 安装器要求 Compose plugin 和 `flock`。它会验证真实 HTTPS `/healthz`，失败时恢复旧配置；不要用临时 `docker run` 代替生成的 Compose 项目。

## NAS：从 WebUI 添加 Agent

无论 Server 使用哪种方式安装，Agent 流程都相同：

1. 打开 `https://你的管理域名`；
2. 进入 **Add Agent**；
3. 填写 Agent 名称、Server URL、VPS 公网 SSH 主机与端口 `2222`；
4. 点击 **Generate command**；
5. 把完整命令粘贴到 NAS。

Agent 安装器生成 Ed25519 密钥、固定 Server 主机公钥、使用一次性 Token 注册，并在成功后清理 Token。它兼容常见 Synology/QNAP PATH、多种 SHA-256 工具和无 `flock` 环境。失败后可重跑同一条命令；不要删除已有 `~/.portloom/agent/data`。

Agent 的完整安装目录（`.env`、`compose.yml`、`data/`）都要持久化和备份，其中 `data/agent.json`、`data/ssh/id_ed25519` 和 `data/ssh/known_hosts` 是身份关键文件。

## 查看状态

Compose 模板方式：

```bash
cd 你的-portloom-compose-目录
docker compose ps -a
docker compose logs --tail=100 server sshd
```

安装脚本默认方式：

```bash
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml ps
docker compose --env-file .env -f compose.yml logs --tail=100 server sshd
```

## 端口和 stream edge 高级参数

安装脚本首次运行时可调整主 edge 或关闭 TCP/UDP 发布：

```bash
DOMAIN='example.com'

# 主 edge 改成本机 8088/8443；公网 80 仍必须转发到 8088
./install-server.sh --domain "$DOMAIN" --version 0.4.1 \
  --http-port 8088 --https-port 8443

# 首次安装时不发布 TCP/UDP
./install-server.sh --domain "$DOMAIN" --version 0.4.1 \
  --disable-tcp-edge
```

Compose 模板对应修改 `.env` 中的 `TM_EDGE_HTTP_ADDR`、`TM_EDGE_HTTPS_ADDR` 与 `TM_TCP_EDGE_BIND_HOST`。路由 `Public port` 留空时跟随主 edge；不要把主 edge 端口再次填进路由，否则会创建冲突的额外 listener。

自定义 Web/TCP 端口需放行 TCP，UDP 路由端口需放行 UDP。修改 HTTP edge 后，公网 80 仍必须能到达它，否则 ACME HTTP-01 证书签发会失败。

## 数据与升级边界

- Compose 模板的数据默认在项目目录的 `data/`；安装器数据默认在 `~/.portloom/server/`。
- 不要删除 Server 的数据库、证书缓存、SSH host key 或 Agent identity。
- 新手模板固定经过验证的 Server/sshd `0.4.1`，数据路径也直接固定在项目的 `./data/`；升级前备份完整项目，再显式修改 `compose.yml` 中的两个版本。
- 安装器升级会固定不可变镜像并自动回滚；Compose 手动升级由你负责备份、改 Tag、`pull`、`up -d` 和健康验证。
- 当前 Agent 安装器不支持在已有目录内跨版本升级；不要通过删除密钥或 identity 强行升级。

从源码构建与全部环境变量见[配置参考](/reference/configuration)，更多手动模板见[模板下载](/reference/templates)。
