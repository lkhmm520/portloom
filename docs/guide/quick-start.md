# 五分钟快速开始

PortLoom 需要两台能运行 Docker 的 Linux 主机：一台有公网 IPv4 的 VPS，以及一台 NAS/内网服务器。公网 VPS 运行 Server 和 WebUI，NAS 只运行 Agent 并主动向外连接。

## 0. 选域名并放行端口

先选一个**你自己的完整管理域名**。根域名和任意子域名都可以，例如：

```text
example.com          A  203.0.113.10
*.example.com        A  203.0.113.10   # 可选，方便以后添加业务域名
```

这里没有固定命名规则，**不需要使用 `portloom.` 前缀**。把所选域名解析到 VPS，并放行 TCP `80`、`443`、`2222`。NAS 无需公网 IP，也无需开放入站端口。

ACME HTTP-01 要求公网 80 能到达 Server 的 HTTP edge；默认安装还要求 VPS 本机 80/443 未被占用。以后创建 TCP/UDP 或自定义 Web 端口路由时，再单独放行对应端口。

## 1. 在 VPS 安装 Server

选择一种方式即可，两种方式运行相同的 Server 与受管 sshd 镜像。

### 方式 A：Compose 模板（配置可见）

打开[用 Compose 模板安装](/guide/compose-install)：

1. 下载 `compose.yml` 和环境变量模板；
2. 将模板重命名为 `.env`，只修改管理域名和管理员 Token；
3. 在 Compose 图形界面启动，或运行 `docker compose up -d`。

这是常规的文件式 Compose 安装，不要求先执行 PortLoom 安装脚本。

### 方式 B：安全安装脚本（自动化更多）

安装器会自动生成随机管理员 Token、解析不可变镜像、验证真实 HTTPS，并在失败时回滚：

```bash
curl -fsSLo install-server.sh https://docs.look4i.com/install-server.sh
less install-server.sh
chmod 0700 install-server.sh
DOMAIN='example.com' # 改成你选定的完整管理域名
./install-server.sh --domain "$DOMAIN" --version 0.4.1
```

完成后，两种方式都应能打开 `https://你的管理域名`。Compose 方式使用 `.env` 中的管理员 Token；安装器方式会在终端显示随机 Token。

## 2. 在 WebUI 添加 Agent

登录 WebUI，进入 **Add Agent**：

1. Agent name：例如 `home-nas`；
2. Server URL：当前 WebUI 的 `https://你的管理域名`；
3. Public Server host：VPS 可从公网访问的域名或 IPv4；
4. SSH tunnel port：默认 `2222`；
5. 点击 **Generate command**。

未执行的安装命令可在令牌列表中删除/撤销；已完成注册的 Agent 不受影响。

## 3. 在 NAS 执行生成的命令

复制 WebUI 生成的**完整命令**到 NAS。Agent 安装器会检查 Docker daemon 与 Compose v2，兼容 `docker compose` 和独立 `docker-compose` v2，也会处理常见 Synology/QNAP PATH、哈希工具和无 `flock` 环境。

这条命令会生成独立 Ed25519 密钥、固定 Server SSH 主机公钥、使用一次性令牌注册，并在成功后从配置删除令牌。失败时修复上方错误后重跑**同一条命令**即可安全续装；不要删除 `~/.portloom/agent/data`。

## 4. 添加第一条 HTTPS 路由

进入 **Routes → Add route**：

| 字段 | 示例 |
| --- | --- |
| Name | Jellyfin |
| Client | home-nas |
| Protocol | HTTPS |
| Public domain | jellyfin.example.com |
| Path prefix | 留空 |
| Public port | 留空（使用主 HTTPS edge） |
| Local host | `127.0.0.1` 或 NAS 可达地址 |
| Local port | `8096` |

若未配置通配符 DNS，先把 `jellyfin.example.com` 单独解析到 VPS。保存后等待 Local、Tunnel、Public 都收敛，再访问 `https://jellyfin.example.com`。

## 5. 接下来

- 需要纯明文服务：Protocol 选 **HTTP**；
- 需要子路径：填写 `/jellyfin`，必要时启用 **Strip path prefix**；
- 需要自定义 Web 端口：填写如 `8443` 并放行对应 TCP 端口；
- 需要 TCP/UDP：选择对应协议并填写 Public port。

详细安装差异见[Docker 安装](/install/docker)，路由规则见[路由管理](/usage/routes)，升级与回滚见[备份、升级与回滚](/operations/backup-upgrade)。
