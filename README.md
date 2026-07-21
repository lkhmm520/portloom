<div align="center">
  <img src="docs/public/logo.svg" width="92" alt="PortLoom logo" />
  <h1>PortLoom</h1>
  <p><strong>用一条 Agent 命令，把内网 Web、TCP 与 UDP 服务发布到公网。</strong></p>
  <p>面向 NAS、家庭实验室与小型自托管环境的反向 SSH 隧道控制平面。</p>
  <p><a href="README.md">简体中文</a> · <a href="README.en.md">English</a></p>
  <p>
    <a href="https://github.com/lkhmm520/portloom/actions/workflows/test.yml"><img alt="Tests" src="https://github.com/lkhmm520/portloom/actions/workflows/test.yml/badge.svg" /></a>
    <a href="https://github.com/lkhmm520/portloom/actions/workflows/docs.yml"><img alt="Docs" src="https://github.com/lkhmm520/portloom/actions/workflows/docs.yml/badge.svg" /></a>
    <a href="https://github.com/lkhmm520/portloom/pkgs/container/portloom-server"><img alt="GHCR" src="https://img.shields.io/badge/GHCR-server%20%7C%20agent%20%7C%20sshd%20%7C%20docs-0f9f72" /></a>
    <img alt="Go" src="https://img.shields.io/badge/Go-1.25.12+-00ADD8?logo=go&logoColor=white" />
  </p>
  <p><a href="https://docs.look4i.com/"><strong>官方文档</strong></a> · <a href="#五分钟开始">快速开始</a> · <a href="https://github.com/lkhmm520/portloom/issues">问题反馈</a></p>
</div>

---

PortLoom 的默认安装路径只需要两台能运行 Docker Compose 的 Linux 主机：一台公网 VPS 运行 Server 与受管 sshd，一台 NAS 或内网主机运行 Agent。Server 原生监听公网 80/443，使用 autocert HTTP-01 获取证书；默认路径不要求预先配置 Caddy、Nginx Proxy Manager 或修改宿主 OpenSSH。安装 Server 后，在 WebUI 生成一条 Agent 命令，即可添加 HTTPS、HTTP、TCP 或 UDP 路由。

内置公网入口支持四种路由协议：**HTTPS**（自动申请证书 + HTTP 跳转）、**HTTP**（纯明文发布，不申请证书）、**TCP** 与 **UDP**（发布指定的 VPS 公网端口，UDP 经隧道内数据报中继转发）。同一个域名可以同时挂多条路由：按路径前缀（如 `example.com/jellyfin`）、按自定义公网端口（如 `example.com:8443`）区分，也可以与管理域名共享（路径前缀方式）。

## 工作原理

```text
                              公网 Docker 主机
浏览器 ──HTTP/HTTPS──> PortLoom Web edge ──> Gateway ──┐
TCP/UDP 客户端 ──────> PortLoom stream edge ───────────┤
                              管理域名 ──> WebUI/API    │ 回环转发
                              授权                      ▼
                               ▼                 managed sshd :2222
                                                   ▲
                                                   │ Agent 主动 OpenSSH -R
内网 HTTP/TCP/UDP 服务 <──────────────────────── Agent
                                            NAS / 内网 Docker 主机
```

| 能力 | 说明 |
| --- | --- |
| **两主机安装** | 公网主机安装 Server 套件，内网主机只安装 Agent |
| **一条 Agent 命令** | WebUI 生成带一次性令牌、固定主机公钥和匹配版本的 Shell 安装命令 |
| **内置 HTTPS** | Server 使用 autocert HTTP-01，只为管理域名和已启用的 HTTPS 路由申请证书并持久化到 `/data/certs` |
| **四协议路由** | HTTPS / HTTP / TCP / UDP；HTTP 不强制跳转 HTTPS，TCP/UDP 直接发布公网端口 |
| **域名复用** | 同一域名支持多条路由：路径前缀、自定义公网端口、HTTP 与 HTTPS 并存 |
| **流量与资源监控** | 仪表盘展示近 60 分钟流量曲线、总计数以及 Server/Agent 的 CPU 与内存；metrics API 另提供每路由计数 |
| **受管 SSH** | 独立 sshd 容器仅允许公钥认证和回环远程转发，不修改宿主 sshd |
| **分层状态** | 本地服务、SSH 隧道和公网 listener 状态分别展示 |
| **小型控制面** | Go Server、Go Agent、SQLite，无外部数据库或 Docker socket |

## 五分钟开始

### 准备

- 公网 VPS 与 NAS/内网主机均可访问 Docker daemon，并使用 Compose v2；
- 你自己选定的完整管理域名已解析到 VPS；根域名或任意子域名都可以，不要求 `portloom.` 前缀；
- VPS 放行 TCP `80`、`443`、`2222`，且 `80/443` 未被占用；NAS 无需开放入站端口。

### 1. 在公网主机安装 Server

推荐直接使用[标准 `compose.yml` 模板](https://docs.look4i.com/guide/compose-install)：下载 `compose.yml` 与 `.env` 模板，只修改管理域名和管理员 Token，然后在 Compose 图形界面启动或执行 `docker compose up -d`。不要求先运行 PortLoom 安装脚本。

如希望自动生成随机凭证、固定不可变镜像并执行 HTTPS readiness/失败回滚，也可选择[安全安装脚本](https://docs.look4i.com/install/docker#方式二-安全安装脚本)。

### 2. 在 WebUI 添加 Agent

打开 `https://你的管理域名` 并登录。进入 **Add Agent**，填写 Agent 名称、Server URL、公网 Server 主机和 SSH 端口，然后点击 **Generate command**。

### 3. 在 NAS 执行一条命令

把 WebUI 生成的完整命令粘贴到 NAS 或内网 Docker 主机。Agent 安装器会生成独立 Ed25519 密钥、固定 Server 主机公钥、用一次性令牌注册并启动 Agent；注册成功后配置中不再保留该令牌。

### 4. 在 WebUI 添加 HTTPS 路由

进入 **Routes → Add route**，选择 Agent，协议使用默认 **HTTPS**，填写公网域名、本地服务地址和端口，例如：

| 字段 | 示例 |
| --- | --- |
| Name | Jellyfin |
| Client | home-nas |
| Protocol | HTTPS |
| Public domain | jellyfin.example.com |
| Local host | 127.0.0.1 |
| Local port | 8096 |

如未配置通配符 DNS，请把该业务域名另行解析到 VPS。保存后等待 Local、Tunnel 与 Public 状态收敛，再访问 `https://jellyfin.example.com`。

详细步骤见[Compose 模板安装](https://docs.look4i.com/guide/compose-install)、[五分钟快速开始](https://docs.look4i.com/guide/quick-start)与[Docker 安装](https://docs.look4i.com/install/docker)。

## 进阶可选集成

默认安装已包含 Server 原生 HTTPS 入口与专用 sshd。只有在公网主机已有入口或有特殊合规/网络要求时，才需要：

- 把现有 Caddy、Nginx 或 Nginx Proxy Manager 接到 PortLoom 的 `8080/8081` 上游；这是遗留/高级集成，外部 Caddy 可选用 `/api/v1/tls/allow` 与 `TM_TLS_ASK_*` 兼容接口；
- 省略受管 sshd，改用经过加固的宿主 OpenSSH 与专用非管理员账户。

这些不是新安装的前置条件。参见[生产环境部署](https://docs.look4i.com/install/production)和[反向代理接入](https://docs.look4i.com/install/reverse-proxy)。

## Docker 镜像

| 组件 | 镜像 |
| --- | --- |
| Server + WebUI + HTTP Gateway | `ghcr.io/lkhmm520/portloom-server:latest` |
| Agent | `ghcr.io/lkhmm520/portloom-agent:latest` |
| 受管 SSH 服务 | `ghcr.io/lkhmm520/portloom-sshd:latest` |
| 文档站 | `ghcr.io/lkhmm520/portloom-docs:latest` |

`vX.Y.Z` Git Tag 先发布不可变的完整版本镜像；发布验收通过后，finalize 工作流才为稳定版本提升 `latest`、主版本与主次版本通道并创建 GitHub Release。预发布不会提升稳定通道。生产环境应固定完整版本。WebUI 仅在 `/api/v1/system` 返回安全镜像 Tag 时，才把同版本传给 Agent 安装器。

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

WebUI 把管理员 Token 保存在当前标签页的 `sessionStorage`，并通过 **HTTP Authorization header using the Bearer scheme** 发送；退出登录或关闭会话后清除。

## 开发与文档

要求 Go 1.25.12+、Node.js 20+，Docker 用于镜像和端到端验证：

```bash
go mod download
npm ci
make check
make test-race
make build
npm run docs:build
```

文档开发可运行 `npm run docs:dev`。中英文入口分别位于[中文文档](https://docs.look4i.com/)与[English docs](https://docs.look4i.com/en/)。

## 当前边界

- 当前 Server 预期单实例运行，不支持 active/active SQLite 写入；
- 管理入口端口可通过 `TM_EDGE_HTTP_ADDR`/`TM_EDGE_HTTPS_ADDR`（或安装器 `--http-port/--https-port`）修改；改掉 80 端口后需保证公网 80 仍可转发到 HTTP 边缘端口，否则 ACME HTTP-01 证书签发会失败；
- UDP 转发经隧道内 TCP 封装（长度前缀帧），适合 DNS/WireGuard 握手等中小数据报场景，吞吐弱于原生 UDP；
- 流量与资源指标保存在内存中，Server 重启后归零；
- `tunnel_group` 当前保存为元数据；需要独立 SSH 主连接时使用多个 Agent/Client。

## 安全

不要把管理监听、Gateway 或自动分配的 SSH 回环端口直接暴露到公网。保持 Agent 出站连接、固定 Server 主机公钥、回环远程转发、只读密钥挂载与最小容器权限。不要在公开 Issue 中粘贴 Token、私钥或完整环境文件。

## 参与项目与发布验收

欢迎提交 Issue 和 Pull Request。提交前运行 `make check`、`make test-race` 与 `npm run docs:build`。首次公开发布完成前，文档站上的安装脚本与 `portloom-sshd` GHCR 镜像不能视为可用发布物；发布后必须按[发布验收清单](https://docs.look4i.com/operations/release-checklist)验证脚本下载、四个镜像、版本固定和完整两主机流程。

## 许可证

项目尚未选择开源许可证。在许可证确定前，代码默认受版权保护；使用、分发或衍生前请先获得授权。
