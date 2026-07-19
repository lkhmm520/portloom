# 五分钟快速开始

你需要两台能运行 Docker Compose 的 Linux 主机：一台有公网 IP 的 VPS，以及一台 NAS/内网服务器。还需要一个管理域名。

## 0. DNS 与防火墙

把管理域名解析到 VPS；可选地添加通配符记录：

```text
portloom.example.com  A  203.0.113.10
*.example.com         A  203.0.113.10
```

VPS 放行 TCP `80`、`443`、`2222`。NAS 无需开放入站端口。ACME HTTP-01 要求公网 80 能到达 Server 的 HTTP edge；默认安装还要求本机 80/443 未被占用。以后创建 TCP/UDP 或自定义 Web 端口路由时，还要单独放行对应端口（UDP 路由放行 UDP）。

## 1. 安装 Server v0.4

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
less install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain portloom.example.com --version 0.4.0
```

安装器启动 `portloom-server` 与 `portloom-sshd`，验证真实 HTTPS `/healthz` 后显示 WebUI 地址和随机管理员令牌。默认 stream edge 绑定 `0.0.0.0`；不需要 TCP/UDP 发布时可在首次安装命令中追加 `--disable-tcp-edge`。

## 2. 在 WebUI 添加 Agent

访问 `https://portloom.example.com` 并输入管理员令牌。进入 **Add Agent**，填写 Agent 名称、HTTPS Server URL、公网 SSH 主机名和端口 `2222`，点击 **Generate command**。

未执行的安装命令可在令牌列表中删除/撤销；已完成注册的 Agent 不受影响。

## 3. 在 NAS 执行生成的命令

安装器会检查 Docker daemon 与 Compose v2，兼容 `docker compose` 和独立 `docker-compose` v2；也会兼容常见 Synology/QNAP PATH、哈希工具、无 `flock` 环境。它生成 Ed25519 密钥、固定 Server 主机公钥、注册并在成功后从配置删除一次性令牌。

安装失败时修复上方错误后，直接重跑**同一条命令**即可安全续装；不要删除 `~/.portloom/agent/data`。

## 4. 添加第一条 HTTPS 路由

在 **Routes → Add route** 中填写：

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

保存后等待 Local、Tunnel、Public 收敛，再访问 `https://jellyfin.example.com`。若未配置通配符 DNS，请先单独添加该域名的 A/AAAA 记录。

## 5. 试用 v0.4 路由能力

- 明文服务：将 Protocol 设为 **HTTP**；不会申请证书或强制跳转。
- 子路径：填写 `/jellyfin`，需要时启用 **Strip path prefix**。
- 自定义 Web 端口：填写如 `8443`，并放行对应 TCP 端口。
- TCP/UDP：选择协议并填写必需的 Public port；确认 stream edge 未被禁用。

详细规则和冲突限制见[路由管理](/usage/routes)，升级与回滚见[备份、升级与回滚](/operations/backup-upgrade)。
