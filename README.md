<div align="center">
  <img src="docs/public/logo.svg" width="92" alt="PortLoom logo" />
  <h1>PortLoom</h1>
  <p><strong>用一条 Agent 命令，把内网 HTTP/HTTPS 服务安全发布到公网。</strong></p>
  <p>面向 NAS、家庭实验室与小型自托管环境的反向 SSH 隧道控制平面。</p>
  <p><a href="README.md">简体中文</a> · <a href="README.en.md">English</a></p>
  <p>
    <a href="https://github.com/lkhmm520/portloom/actions/workflows/test.yml"><img alt="Tests" src="https://github.com/lkhmm520/portloom/actions/workflows/test.yml/badge.svg" /></a>
    <a href="https://github.com/lkhmm520/portloom/actions/workflows/docs.yml"><img alt="Docs" src="https://github.com/lkhmm520/portloom/actions/workflows/docs.yml/badge.svg" /></a>
    <a href="https://github.com/lkhmm520/portloom/pkgs/container/portloom-server"><img alt="GHCR" src="https://img.shields.io/badge/GHCR-server%20%7C%20agent%20%7C%20sshd%20%7C%20docs-0f9f72" /></a>
    <img alt="Go" src="https://img.shields.io/badge/Go-1.25.12+-00ADD8?logo=go&logoColor=white" />
  </p>
  <p><a href="https://docs.961121.xyz/"><strong>官方文档</strong></a> · <a href="#五分钟开始">快速开始</a> · <a href="https://github.com/lkhmm520/portloom/issues">问题反馈</a></p>
</div>

---

PortLoom 的默认安装路径只需要两台能运行 Docker Compose 的 Linux 主机：一台公网 VPS 运行 Server、受管 sshd 与 Caddy，一台 NAS 或内网主机运行 Agent。安装 Server 后，在 WebUI 生成一条 Agent 命令，再在 WebUI 添加 HTTP 路由即可；默认路径不要求预先配置 Nginx Proxy Manager 或修改宿主 OpenSSH。

当前内置公网入口完整支持按域名发布 **HTTP/HTTPS** 服务。TCP 字段只作为兼容元数据保留，不会自动创建公网 TCP 监听，也不会在 WebUI 中显示为 published/healthy。

## 工作原理

```text
                         公网 Docker 主机
浏览器 ──HTTPS──> Caddy ──Host──> PortLoom Gateway
                    │                  │
                    │ 管理域名         ▼
                    └──────────> Server + SQLite
                                         │ 授权
                                         ▼
                                  managed sshd :2222
                                         ▲
                                         │ Agent 发起 OpenSSH -R
                                         │
内网 HTTP 服务 <──────── Agent <────────┘
                         NAS / 内网 Docker 主机
```

| 能力 | 说明 |
| --- | --- |
| **两主机安装** | 公网主机安装 Server 套件，内网主机只安装 Agent |
| **一条 Agent 命令** | WebUI 生成带一次性令牌、固定主机公钥和匹配版本的 Shell 安装命令 |
| **内置 HTTPS** | Caddy 为管理域名和已启用的 HTTP 路由按需申请证书 |
| **受管 SSH** | 独立 sshd 容器仅允许公钥认证和回环远程转发，不修改宿主 sshd |
| **分层状态** | 本地服务、SSH 隧道和 HTTP 公网发布状态分别展示 |
| **小型控制面** | Go Server、Go Agent、SQLite，无外部数据库或 Docker socket |

## 五分钟开始

### 准备

- 公网 VPS 与 NAS/内网主机均已安装 Docker Engine 24+ 和 Compose v2；
- 管理域名（例如 `portloom.example.com`）已解析到 VPS；
- VPS 放行 TCP `80`、`443`、`2222`，且 `80/443` 未被占用；NAS 无需开放入站端口。

### 1. 在公网主机安装 Server

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
less install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain portloom.example.com
```

安装器启动 `portloom-server`、`portloom-sshd` 和 `portloom-caddy`，最后输出 WebUI 地址与随机管理员令牌。

### 2. 在 WebUI 添加 Agent

打开 `https://portloom.example.com` 并登录。进入 **Add Agent**，填写 Agent 名称、Server URL、公网 Server 主机和 SSH 端口，然后点击 **Generate command**。

### 3. 在 NAS 执行一条命令

把 WebUI 生成的完整命令粘贴到 NAS 或内网 Docker 主机。Agent 安装器会生成独立 Ed25519 密钥、固定 Server 主机公钥、用一次性令牌注册并启动 Agent；注册成功后配置中不再保留该令牌。

### 4. 在 WebUI 添加 HTTP 路由

进入 **Routes → Add HTTP route**，选择 Agent，填写公网域名、本地服务地址和端口，例如：

| 字段 | 示例 |
| --- | --- |
| Name | Jellyfin |
| Client | home-nas |
| Public domain | jellyfin.example.com |
| Local host | 127.0.0.1 |
| Local port | 8096 |

如未配置通配符 DNS，请把该业务域名另行解析到 VPS。保存后等待 Local、Tunnel 与 Public 状态收敛，再访问 `https://jellyfin.example.com`。

详细步骤见[五分钟快速开始](https://docs.961121.xyz/guide/quick-start)与[Docker 安装](https://docs.961121.xyz/install/docker)。

## 进阶可选集成

默认安装已包含 Caddy 与专用 sshd。只有在公网主机已有入口或有特殊合规/网络要求时，才需要：

- 把现有 Caddy、Nginx 或 Nginx Proxy Manager 接到 PortLoom 的 `8080/8081` 上游；
- 省略受管 sshd，改用经过加固的宿主 OpenSSH 与专用非管理员账户。

这些不是新安装的前置条件。参见[生产环境部署](https://docs.961121.xyz/install/production)和[反向代理接入](https://docs.961121.xyz/install/reverse-proxy)。

## Docker 镜像

| 组件 | 镜像 |
| --- | --- |
| Server + WebUI + HTTP Gateway | `ghcr.io/lkhmm520/portloom-server:latest` |
| Agent | `ghcr.io/lkhmm520/portloom-agent:latest` |
| 受管 SSH 服务 | `ghcr.io/lkhmm520/portloom-sshd:latest` |
| 文档站 | `ghcr.io/lkhmm520/portloom-docs:latest` |

稳定版 `vX.Y.Z` Git Tag 会发布完整语义化版本、主/次版本、`sha-*` 和 `latest`；预发布不覆盖 `latest`，手动发布只生成 `edge` 与 `sha-*`。生产环境应固定完整版本。WebUI 仅在 `/api/v1/system` 返回安全镜像 Tag 时，才把同版本传给 Agent 安装器。

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

文档开发可运行 `npm run docs:dev`。中英文入口分别位于[中文文档](https://docs.961121.xyz/)与[English docs](https://docs.961121.xyz/en/)。

## 当前边界

- 当前 Server 预期单实例运行，不支持 active/active SQLite 写入；
- 内置公网入口只完整支持 HTTP/HTTPS Host 路由；已有 TCP 记录仅为控制平面元数据；
- `tunnel_group` 当前保存为元数据；需要独立 SSH 主连接时使用多个 Agent/Client。

## 安全

不要把管理监听、Gateway 或自动分配的 SSH 回环端口直接暴露到公网。保持 Agent 出站连接、固定 Server 主机公钥、回环远程转发、只读密钥挂载与最小容器权限。不要在公开 Issue 中粘贴 Token、私钥或完整环境文件。

## 参与项目与发布验收

欢迎提交 Issue 和 Pull Request。提交前运行 `make check`、`make test-race` 与 `npm run docs:build`。首次公开发布完成前，文档站上的安装脚本与 `portloom-sshd` GHCR 镜像不能视为可用发布物；发布后必须按[发布验收清单](https://docs.961121.xyz/operations/release-checklist)验证脚本下载、四个镜像、版本固定和完整两主机流程。

## 许可证

项目尚未选择开源许可证。在许可证确定前，代码默认受版权保护；使用、分发或衍生前请先获得授权。
