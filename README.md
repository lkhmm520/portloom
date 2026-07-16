<div align="center">
  <img src="docs/public/logo.svg" width="92" alt="PortLoom logo" />
  <h1>PortLoom</h1>
  <p><strong>把反向 SSH 隧道织成可管理、可观察、可回滚的基础设施。</strong></p>
  <p>面向 NAS、家庭实验室与小型自托管环境的轻量控制平面。</p>

  <p>
    <a href="README.md">简体中文</a> · <a href="README.en.md">English</a>
  </p>

  <p>
    <a href="https://github.com/lkhmm520/portloom/actions/workflows/test.yml"><img alt="Tests" src="https://github.com/lkhmm520/portloom/actions/workflows/test.yml/badge.svg" /></a>
    <a href="https://github.com/lkhmm520/portloom/actions/workflows/docs.yml"><img alt="Docs" src="https://github.com/lkhmm520/portloom/actions/workflows/docs.yml/badge.svg" /></a>
    <a href="https://github.com/lkhmm520/portloom/pkgs/container/portloom-server"><img alt="GHCR" src="https://img.shields.io/badge/GHCR-server%20%7C%20agent%20%7C%20docs-0f9f72" /></a>
    <img alt="Go" src="https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white" />
  </p>

  <p>
    <a href="https://docs.961121.xyz/"><strong>官方文档</strong></a> ·
    <a href="#五分钟开始">快速开始</a> ·
    <a href="https://github.com/lkhmm520/portloom/issues">问题反馈</a>
  </p>
</div>

---

PortLoom 不替换你已有的 Nginx Proxy Manager、证书、DNS 或 OpenSSH。它负责保存期望状态、分配安全的回环端口、协调 NAS Agent 建立 `ssh -R` 转发，并把本地服务、SSH 隧道与公网收敛状态分层展示。

## 为什么选择 PortLoom

| 能力 | 说明 |
| --- | --- |
| **保留现有入口** | NPM 继续负责 TLS；业务域名统一转发到 Host Gateway |
| **分层可观察** | 本地服务、SSH 转发、desired/observed revision 分开判断 |
| **最小权限** | 专用 SSH 账户、固定主机指纹、loopback 转发、非 root 只读容器 |
| **安全注册** | 一次性、可过期 Token；每个 Agent 使用独立长期凭据 |
| **低运维成本** | 单 Go Server、单 Go Agent、SQLite，无外部数据库 |
| **迁移可回滚** | 新旧隧道可并行验证，逐域名切换，不强行接管 DNS/NPM |

## 工作原理

```text
Browser ── HTTPS ──> NPM ── Host ──> PortLoom Gateway :8081
                       │                      │
                       │ admin               ▼
                       └──────────> Server + SQLite
                                              ▲
                                              │ desired / observed
NAS service <── Agent <──── OpenSSH -R ───────┘
```

- **Server `:8080`**：控制台、管理 API、注册与 Agent 心跳；
- **Gateway `:8081`**：按 HTTP `Host` 选择已启用路由；
- **Agent**：探测 NAS 本地服务，维护 OpenSSH ControlMaster 与远程转发；
- **NPM**：继续负责公网证书、HTTPS 重定向与域名入口。

## 五分钟开始

### 1. 启动 Server

```bash
mkdir -p portloom/server && cd portloom/server
curl -LO https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/docker-compose.server.yml
curl -Lo server.env https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/server.env.example
mkdir -p data/server
sudo chown -R 65532:65532 data/server
openssl rand -hex 32  # 写入 server.env 的 TM_ADMIN_TOKEN
docker compose --env-file server.env -f docker-compose.server.yml up -d
curl --fail http://127.0.0.1:8080/healthz
```

### 2. 注册 NAS Agent

在控制台创建一次性注册令牌，然后：

```bash
mkdir -p portloom/agent/{data/agent,secrets} && cd portloom/agent
curl -LO https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/docker-compose.agent.yml
curl -Lo agent.env https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/agent.env.example
# 填写控制面、SSH、令牌和密钥路径
docker compose --env-file agent.env -f docker-compose.agent.yml up -d
```

注册成功后，从 `agent.env` 删除 `TM_ENROLLMENT_TOKEN`。生产环境还需要配置受限 SSH 账户、固定 `known_hosts`、NPM 管理域名与业务域名；请按[生产部署文档](https://docs.961121.xyz/install/production)操作。

## 从源码运行 Server

```bash
make build
export TM_ADMIN_TOKEN="$(openssl rand -hex 32)"
export TM_LISTEN_ADDR=127.0.0.1:8080
export TM_GATEWAY_ADDR=127.0.0.1:8081
export TM_DATABASE_PATH="$(pwd)/data/portloom.db"
export TM_WEB_DIR="$(pwd)/web"
mkdir -p "$(pwd)/data"
./bin/portloom-server
```

控制台把管理员 Token 保存在当前标签页的 `sessionStorage` 中，并通过 **HTTP Authorization header using the Bearer scheme** 发送；关闭会话或退出登录后清除。

## Docker 镜像

| 组件 | 镜像 |
| --- | --- |
| Server + Web 控制台 | `ghcr.io/lkhmm520/portloom-server:latest` |
| NAS Agent | `ghcr.io/lkhmm520/portloom-agent:latest` |
| 官方文档站 | `ghcr.io/lkhmm520/portloom-docs:latest` |

严格的稳定版 `vX.Y.Z` Git Tag 会发布语义化版本、主版本/次版本、`sha-*` 和 `latest`；预发布 Tag 不会覆盖 `latest`，手动发布仅生成 `edge` 与 `sha-*`。生产环境建议固定完整版本。

## 文档

> 官网当前通过PortLoom自托管；如需启用GitHub Pages备用发布，请在仓库变量中设置 `ENABLE_GITHUB_PAGES=true`。

- [认识 PortLoom](https://docs.961121.xyz/guide/what-is-portloom)
- [Docker 安装](https://docs.961121.xyz/install/docker)
- [生产环境部署](https://docs.961121.xyz/install/production)
- [配置参考](https://docs.961121.xyz/reference/configuration)
- [HTTP API](https://docs.961121.xyz/reference/api)
- [故障排查](https://docs.961121.xyz/operations/troubleshooting)
- [Compose 模板下载](https://docs.961121.xyz/reference/templates)

本地运行文档：

开发预览：

```bash
npm ci
npm run docs:dev
```

或者构建静态站与容器：

```bash
npm ci
npm run docs:build
docker build -f Dockerfile.docs -t portloom-docs:local .
```

## 开发与验证

要求 Go 1.24+、Node.js 20+，Docker 可选：

```bash
go mod download
npm ci
make check
make test-race
make build
make docker-build VERSION=local
```

## 当前边界

- 当前 Server 预期单实例运行，不支持 active/active SQLite 写入；
- Gateway 仅处理 HTTP Host 路由；TCP 路由目前主要保存控制平面元数据；
- `tunnel_group` 当前保存为元数据。需要 Web/媒体独立 SSH 主连接时，请使用 `examples/docker-compose.dual-agent.yml`。

## 安全

不要把管理端口或分配的 SSH 回环端口直接暴露到公网。使用专用非管理员 SSH 账户、`GatewayPorts no`、只读密钥挂载和经过核验的 `known_hosts`。安全问题请避免在公开 Issue 中粘贴 Token、私钥或完整环境文件。

## 参与项目

欢迎提交 Issue 和 Pull Request。提交前请运行 `make check`、`make test-race` 与 `npm run docs:build`。中英文文档目录保持同结构，Compose 示例以仓库 `examples/` 为唯一来源。

## 许可证

项目尚未选择开源许可证。在许可证确定前，代码默认受版权保护；使用、分发或衍生前请先获得授权。
