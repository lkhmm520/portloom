# Docker 安装

PortLoom 分别安装在两台主机上。不要在 NAS 上安装 Server，也不要在 VPS 上用 Agent 代替 Server。

```text
公网VPS：Server + 专用sshd + Caddy
                 ▲
                 │ Agent主动建立加密隧道
                 │
内网NAS：Agent → 本地服务
```

## 要求

两台主机都需要 Docker Engine 24+ 和 Compose v2。公网主机还需要：

- 一个解析到该主机的管理域名；
- TCP 80、443和2222可从公网访问；
- 80/443未被其他程序占用。

如果公网主机已有Caddy、Nginx或NPM，请不要运行简易安装器，以免端口冲突。改用[生产环境部署](/install/production)和[反向代理接入](/install/reverse-proxy)。

## 公网主机：Server

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain portloom.example.com
```

默认安装目录是`~/.portloom/server`：

```text
compose.yml       三个服务的Compose配置
.env              镜像、端口和随机令牌（0600）
server-data/      SQLite数据库
ssh-hostkeys/     持久化的Server SSH主机密钥
ssh-auth/         由Server重建的Agent授权文件
caddy-data/       HTTPS证书和Caddy状态
```

常用命令：

```bash
cd ~/.portloom/server
docker compose ps
docker compose logs --tail=100
docker compose pull
docker compose up -d
```

不要删除`server-data`和`ssh-hostkeys`。前者保存配置，后者决定Agent信任的Server身份。

## 内网主机：Agent

推荐从WebUI的 **Add Agent** 页面复制命令。命令调用公开的`install-agent.sh`并带上一次性注册信息。

默认安装目录是`~/.portloom/agent`：

```text
compose.yml       Agent Compose配置
.env              Server和SSH地址（注册成功后不再含一次性令牌）
data/agent.json   持久Agent身份
 data/ssh/         Agent私钥和固定的Server主机公钥
```

常用命令：

```bash
cd ~/.portloom/agent
docker compose ps
docker compose logs --tail=100
docker compose pull
docker compose up -d
```

## 安装器参数

```bash
./install-server.sh --help
./install-agent.sh --help
```

生产环境应使用`--version`固定发布标签。`latest`适合首次体验，不适合无人值守升级。

## 从源码构建

```bash
git clone https://github.com/lkhmm520/portloom.git
cd portloom
make docker-build VERSION=local
docker build -f Dockerfile.sshd -t portloom-sshd:local .
docker build -f Dockerfile.docs -t portloom-docs:local .
```
