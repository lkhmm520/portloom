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

VPS 防火墙放行 TCP `80`、`443` 和 `2222`。NAS 不需要开放入站端口。

## 1. 在公网主机安装 Server

先下载并查看脚本，再执行：

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
less install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain portloom.example.com
```

安装器会启动三个容器：

- `portloom-server`：WebUI、API 和路由网关；
- `portloom-sshd`：PortLoom 专用的受限 SSH 入口；
- `portloom-caddy`：自动申请管理域名和业务域名的 HTTPS 证书。

结束时会显示 WebUI 地址和随机管理员令牌。

## 2. 打开 WebUI

访问 `https://portloom.example.com`，输入安装器显示的管理员令牌。

进入 **Add Agent**，填写：

- Agent name：例如 `home-nas`；
- Server URL：`https://portloom.example.com`；
- Public Server host：`portloom.example.com`；
- SSH tunnel port：默认 `2222`。

点击 **Generate command**，网页会生成一条只显示一次的安装命令。

## 3. 在 NAS 安装 Agent

把上一步的完整命令粘贴到 NAS 或内网 Docker 主机执行。安装器会自动：

- 生成独立 Ed25519 密钥；
- 写入Server主机公钥，不使用不可信的`ssh-keyscan`结果；
- 使用一次性令牌注册；
- 上传Agent公钥并更新受限授权文件；
- 启动Agent，注册成功后从配置中删除一次性令牌。

几秒后，WebUI 的 Clients 页面会显示新主机。

## 4. 添加第一条路由

在 **Routes → Add HTTP route** 中填写：

| 字段 | 示例 |
| --- | --- |
| Name | Jellyfin |
| Client | home-nas |
| Protocol | HTTP |
| Public domain | jellyfin.example.com |
| Local host | 127.0.0.1，或NAS局域网服务地址 |
| Local port | 8096 |

保存后等待本地服务和隧道状态变为绿色，再访问 `https://jellyfin.example.com`。

::: tip DNS
如果没有配置通配符解析，请单独把 `jellyfin.example.com` 的 A/AAAA 记录指向VPS。Caddy只会为WebUI中已启用的HTTP路由申请证书。
:::

配置文件和升级方式见[Docker安装](/install/docker)。已有反向代理或需要手动审计全部Compose参数时，阅读[生产环境部署](/install/production)。
