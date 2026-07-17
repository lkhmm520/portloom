# 生产环境部署

快速开始适合一台 80/443 空闲的 VPS。生产部署前先决定公网入口：

- 80/443 空闲：使用简易安装器，让 PortLoom Server 原生终止 HTTPS；
- 已有 Caddy、Nginx 或 NPM：使用 8080/8081 作为外部反向代理上游，这是兼容旧部署的高级集成；
- 有合规要求：固定镜像版本，逐项审计 Compose、capability 和卷权限。

## 原生入口的端口与数据流

| 端口 | 默认监听 | 用途 |
| --- | --- | --- |
| 80/443 | PortLoom Server 公网地址 | ACME HTTP-01、HTTPS WebUI 和 HTTP 业务域名 |
| 2222 | 受管 sshd 公网地址 | Agent 主动建立反向隧道 |
| 8080 | 127.0.0.1 | Server WebUI 和 API 的内部监听器 |
| 8081 | 127.0.0.1 | Host 路由 Gateway 的内部监听器 |
| 20000–29999 | 127.0.0.1 | 自动分配的 SSH 回环端口 |

Agent 所在网络只需要出站访问 Server 的 443 和 2222。公网 80 必须保持可达，供 ACME HTTP-01 签发和续期使用。

Server 需要 `NET_BIND_SERVICE` capability 才能以非 root 身份绑定 80/443。简易安装器生成的 Compose 先丢弃全部 capability，再只添加这一项；手写 Compose 时不要遗漏，也不要改为特权容器。

## 固定镜像版本

```bash
./install-server.sh --domain portloom.example.com --version 0.3.0
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml config
docker compose up -d
docker compose ps
```

保持 Compose 项目名和卷路径。不要使用临时 `docker run` 重建数据库容器。

## 受管 SSH 边界

`portloom-sshd` 是独立容器，不修改宿主机 `/etc/ssh/sshd_config`。它只允许 Ed25519 公钥认证和绑定 `127.0.0.1:*` 的 `ssh -N -R` 远程转发；拒绝 Shell、TTY、X11、Agent 转发和用户 RC。

Server 对 `ssh-auth` 卷有写权限，sshd 只读。sshd 对 `ssh-hostkeys` 卷有写权限，Server 只读。Server 启动时从 SQLite 重建 `authorized_keys`。应保留 `ssh-hostkeys/ssh_host_ed25519_key`。

## 数据、证书和权限

至少备份：

```text
server-data/portloom.db
server-data/certs/
ssh-hostkeys/
.env
```

`/data/certs` 是 autocert 的持久证书缓存，在默认安装中对应 `server-data/certs/`。`server-data` 和 `ssh-auth` 由 UID/GID 65532 写入；不要把目录改成 `0777`。

## 已有公网入口（高级/兼容模式）

不要启用原生 `TM_EDGE_HTTP_ADDR`/`TM_EDGE_HTTPS_ADDR`，并确保 Server 的 8080/8081 只监听回环或受防火墙保护的私网地址。管理域名转发到 8080；业务域名转发到 8081 并保留 Host，详见[反向代理接入](/install/reverse-proxy)。受管 sshd 仍可使用 2222。

外部 Caddy 如仍使用 on-demand TLS `ask`，可启用可选的 `TM_TLS_ASK_TOKEN` 和 `TM_TLS_ASK_ADDR` 兼容端点。它不是原生入口的证书签发路径。

## 上线验证

验证 HTTPS WebUI、HTTP 到 HTTPS 跳转、2222 拒绝普通命令、Agent/Local/Tunnel 状态、Host 保留和重启恢复。确认只有 `TM_PUBLIC_HOST` 和已启用 HTTP 路由域名能触发签发，并演练恢复数据库、证书缓存和 SSH 主机密钥。
