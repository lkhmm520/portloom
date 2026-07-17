# Docker 安装

PortLoom 分别安装在两台主机上。不要在 NAS 上安装 Server，也不要在 VPS 上用 Agent 代替 Server。

```text
公网 VPS：Server（原生 HTTPS 入口）+ 专用 sshd
                 ▲
                 │ Agent 主动建立加密隧道
                 │
内网 NAS：Agent → 本地服务
```

## 要求

两台主机都需要 Docker Engine 24+ 和 Compose v2。公网主机还需要：

- 一个解析到该主机的管理域名；
- TCP 80、443 和 2222 可从公网访问；
- 80/443 未被其他程序占用，以便 Server 完成 ACME HTTP-01 验证并提供 HTTPS。

如果公网主机已有 Caddy、Nginx 或 NPM，请不要运行简易安装器，以免端口冲突。改用[生产环境部署](/install/production)和[反向代理接入](/install/reverse-proxy)，让外部入口转发到 8080/8081。

## 公网主机：Server

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain portloom.example.com
```

简易安装只运行 `portloom-server` 和 `portloom-sshd`。Server 直接监听公网 80/443，使用 autocert 获取证书；Compose 为它添加 `NET_BIND_SERVICE`，不会安装 Caddy、Nginx 或 NPM。

默认安装目录是 `~/.portloom/server`：

```text
compose.yml       两个服务的 Compose 配置
.env              镜像、端口、管理域名和随机令牌（0600）
server-data/      SQLite 数据库和 certs/ 证书缓存
ssh-hostkeys/     持久化的 Server SSH 主机密钥
ssh-auth/         由 Server 重建的 Agent 授权文件
```

容器内证书缓存路径是 `/data/certs`，对应 `server-data/certs/`。不要删除 `server-data` 或 `ssh-hostkeys`：前者保存数据库和 ACME 证书，后者决定 Agent 信任的 Server 身份。

从包含 Caddy 的 v0.2.x 简易安装升级时，不要直接覆盖 Compose；请按[备份、升级与回滚](/operations/backup-upgrade)使用显式的 `--migrate-native-edge` 流程。

常用命令：

```bash
cd ~/.portloom/server
docker compose ps
docker compose logs --tail=100
```

不要用 `docker compose pull && docker compose up -d` 升级简易安装；它会绕过候选配置、备份、HTTPS readiness 与自动回滚。升级应使用下方固定新 `--version` 的安装器重跑流程。

## 内网主机：Agent

推荐从 WebUI 的 **Add Agent** 页面复制命令。命令调用公开的 `install-agent.sh` 并带上一次性注册信息。默认安装目录是 `~/.portloom/agent`，其中 `data/agent.json` 保存身份，`data/ssh/` 保存 Agent 私钥和固定的 Server 主机公钥。

## 安装器参数

```bash
./install-server.sh --help
./install-agent.sh --help
```

生产环境应使用 `--version` 固定发布标签。`latest` 适合首次体验，不适合无人值守升级。安装器会把解析后的 Server/sshd 不可变镜像 ID 写入 `.env`，Compose 只使用这些 ID；相同引用的幂等重跑不会再次 pull，也不会因为本地 `latest` 被改指而静默换镜像。旧安装首次重跑时会从仍在运行且属于同一安装目录的容器迁移镜像 ID；若容器与 ID 都不存在则拒绝猜测。升级请传入不同的固定 `--version`，安装器会保留管理员令牌和持久化数据，并在更新镜像后重新验证 HTTPS。

## 从源码构建

```bash
git clone https://github.com/lkhmm520/portloom.git
cd portloom
make docker-build VERSION=local
docker build -f Dockerfile.sshd -t portloom-sshd:local .
docker build -f Dockerfile.docs -t portloom-docs:local .
```
