# 五分钟快速开始

你需要两台能运行 Docker Compose 的 Linux 主机：一台有公网 IP 的 VPS，以及一台 NAS 或内网服务器。还需要一个域名。

## 0. 先准备 DNS 和防火墙

把管理域名解析到 VPS，例如：

```text
portloom.example.com  A  203.0.113.10
```

如果所有服务都放在同一主域名下，可以再添加一次通配符解析：

```text
*.example.com  A  203.0.113.10
```

VPS 防火墙放行 TCP `80`、`443` 和 `2222`。NAS 不需要开放入站端口。ACME HTTP-01 验证要求公网能够访问端口 80，且 80/443 不能被其他程序占用。

## 1. 在公网主机安装 Server

先下载并查看脚本，再执行：

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
less install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain portloom.example.com
```

安装器会启动两个容器：

- `portloom-server`：WebUI、API、路由网关和公网 80/443 原生 HTTPS 入口；
- `portloom-sshd`：PortLoom 专用的受限 SSH 入口。

Server 使用 autocert 和 ACME HTTP-01 自动申请及续期证书，并把证书缓存持久化到 Server 数据目录内的 `/data/certs`。安装器会为 Server 添加绑定 80/443 所需的 `NET_BIND_SERVICE` capability；不会安装 Caddy、Nginx 或 NPM。

结束时会显示 WebUI 地址和随机管理员令牌。

## 2. 打开 WebUI

访问 `https://portloom.example.com`，输入安装器显示的管理员令牌。

进入 **Add Agent**，填写 Agent 名称、HTTPS Server URL、公网 Server 主机名和 SSH 端口 `2222`，然后点击 **Generate command**。

## 3. 在 NAS 安装 Agent

把生成的完整命令粘贴到 NAS 或内网 Docker 主机执行。安装器会生成 Ed25519 密钥、固定 Server 主机公钥、使用一次性令牌注册、上传 Agent 公钥，并在成功后删除一次性注册令牌。

几秒后，WebUI 的 Clients 页面会显示新主机。

## 4. 添加第一条路由

在 **Routes → Add HTTP route** 中填写：

| 字段 | 示例 |
| --- | --- |
| Name | Jellyfin |
| Client | home-nas |
| Protocol | HTTP |
| Public domain | jellyfin.example.com |
| Local host | 127.0.0.1，或 NAS 局域网服务地址 |
| Local port | 8096 |

保存后等待本地服务和隧道状态变为绿色，再访问 `https://jellyfin.example.com`。

::: tip DNS 与证书
如果没有配置通配符解析，请单独把 `jellyfin.example.com` 的 A/AAAA 记录指向 VPS。内置入口只会为 `TM_PUBLIC_HOST` 和 WebUI 中已启用的 HTTP 路由域名申请证书；未知域名不会被授权。
:::

配置文件和升级方式见[Docker 安装](/install/docker)。已有反向代理或需要手动审计全部 Compose 参数时，阅读[生产环境部署](/install/production)。
