# Docker 安装

## 要求

- Docker Engine 24+ 与 Compose v2；
- Server 主机具备 OpenSSH 服务；
- NAS 能主动连接 Server 的 SSH 与 HTTPS；
- 反向代理能访问 `8080/8081`；
- 数据目录可由 UID/GID `65532` 读写。

## 拉取镜像

```bash
docker pull ghcr.io/lkhmm520/portloom-server:latest
docker pull ghcr.io/lkhmm520/portloom-agent:latest
docker pull ghcr.io/lkhmm520/portloom-docs:latest
```

正式环境建议固定版本标签，而不是长期使用 `latest`。

## 使用 Compose

模板位于仓库 [`examples/`](https://github.com/lkhmm520/portloom/tree/main/examples)，也可在[模板下载](/reference/templates)直接下载。

```bash
docker compose --env-file server.env -f docker-compose.server.yml config
docker compose --env-file server.env -f docker-compose.server.yml up -d
```

Agent 同理使用 `agent.env`。Compose 默认启用只读根文件系统、丢弃全部 Linux capabilities，并以非 root 用户运行。

## 从源码构建

```bash
git clone https://github.com/lkhmm520/portloom.git
cd portloom
make docker-build VERSION=local
docker build -f Dockerfile.docs -t portloom-docs:local .
```
