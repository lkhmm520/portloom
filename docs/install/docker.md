# Docker 安装

PortLoom 分别安装在两台主机：公网 VPS 运行 Server + 受管 sshd，NAS/内网主机运行 Agent 并主动连接公网。

## 要求

- 两台主机可访问 Docker daemon，并使用 Compose v2；Agent 接受 `docker compose` 或独立 `docker-compose` v2，Server 安装器只接受 Compose plugin；
- 管理域名解析到 VPS；
- 默认安装放行 TCP 80/443/2222，且本机 80/443 空闲；
- NAS 只需出站访问 Server HTTPS 和 SSH，无需入站端口。

公网主机已有 Caddy/Nginx/NPM 时，不要直接运行默认安装命令占用 80/443；阅读[生产环境部署](/install/production)和[反向代理接入](/install/reverse-proxy)。

## 公网主机：Server v0.4

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
chmod 0700 install-server.sh
./install-server.sh \
  --domain portloom.example.com \
  --version 0.4.0
```

Server 安装器要求 `flock`；Agent 安装器才提供无 `flock` 的目录锁兼容。生成目录：

```text
compose.yml       Server + sshd 的 Compose 配置
.env              固定镜像 ID、端口、管理域名、随机管理员令牌（0600）
server-data/      SQLite 与 certs/ 证书缓存
ssh-hostkeys/     Agent 已固定信任的 Server SSH 身份
ssh-auth/         Server 从 SQLite 重建的 Agent 授权文件
```

不要删除 `server-data` 或 `ssh-hostkeys`，也不要用临时 `docker run` 重建服务。简易安装的升级必须重跑新版本安装器，让它执行候选配置、备份、真实 HTTPS readiness 和自动回滚。

## v0.4 端口与 stream edge 参数

```bash
# 主 edge 改成本机 8088/8443；公网 80 仍必须转发到 8088
./install-server.sh --domain portloom.example.com --version 0.4.0 \
  --http-port 8088 --https-port 8443

# 首次安装时不发布 TCP/UDP
./install-server.sh --domain portloom.example.com --version 0.4.0 \
  --disable-tcp-edge

# 首次安装时只在指定 IP 发布 TCP/UDP
PORTLOOM_TCP_EDGE_BIND_HOST=192.0.2.10 \
  ./install-server.sh --domain portloom.example.com --version 0.4.0 \
  --enable-tcp-edge
```

`--enable-tcp-edge` 仅为兼容保留；v0.4 默认绑定 `0.0.0.0`，但安装器只在该参数出现时读取自定义 `PORTLOOM_TCP_EDGE_BIND_HOST`。这些开关可靠地定义首次安装；已有 `.env` 的非空值会被安装器保留，不要把重跑参数当成通用切换器。

路由 `Public port` 留空跟随主 edge。使用上述自定义端口时不要在路由中再填写 8443，否则它表示额外 listener 并与主端口冲突。HTTP 308 会广告公网 `:8443`；只做 `public 443 -> local 8443` 不足以完成跳转，还必须公开/映射公网 8443，保持配置端口与公网端口一致，或让前置代理接管并正确重写跳转。自定义 Web/TCP 端口需放行 TCP，UDP 路由端口需放行 UDP。

## 内网主机：Agent

从 WebUI **Add Agent** 复制命令，不要手抄一次性 Token。默认目录 `~/.portloom/agent`；必须持久化和备份完整安装目录（含 `.env`、`compose.yml` 与 `data/`），其中 `data/agent.json`、`data/ssh/id_ed25519` 和 `data/ssh/known_hosts` 是身份关键文件。

v0.4 Agent 安装器会补充常见 Synology/QNAP PATH，验证 Docker daemon，接受 Compose plugin 或独立 `docker-compose` v2，支持多种 SHA-256 工具，并在缺少 `flock` 时使用目录锁。失败后可重跑同一条命令续装；不要删除已有 identity。若残留 `$home/.install.lock.d`，必须先确认没有并发安装进程才能删除。

同命令重跑只用于同版本续装/恢复。当前安装器会拒绝把已有 Agent 目录切换到不同 `--version`，也没有已发布的安装器内跨版本升级事务；不要用删除 `agent.json`、私钥或盲改不可变镜像 ID 的方式强行升级。

## 查看状态

```bash
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml ps
docker compose --env-file .env -f compose.yml logs --tail=100 server
```

生产固定 `--version`。`latest` 适合首次体验，不适合无人值守升级。安装器把解析后的不可变镜像 ID 写入 `.env`；同引用重跑不会静默跟随本地已移动的 `latest`。

## 从源码构建

```bash
git clone https://github.com/lkhmm520/portloom.git
cd portloom
make docker-build VERSION=local
```
