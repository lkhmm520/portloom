# 生产环境部署

## 先选择公网入口

| 模式 | 适用场景 | v0.4 能力 |
| --- | --- | --- |
| 原生 edge（推荐） | 公网 80 到达配置的 HTTP listener，且配置的 HTTPS 端口能以**相同 advertised 端口**从公网访问 | 完整 HTTP/HTTPS、路径、extra Web 端口、管理域名子路径、自动证书 |
| 外部 Caddy/Nginx/NPM | 已有入口必须继续占用 80/443 | 8080 管理 + 8081 传统 Gateway；TLS/跳转由外部入口负责，自定义 Web public port 不可用 |

TCP/UDP stream edge 与原生 Web edge 独立；只要 `TM_TCP_EDGE_BIND_HOST` 未关闭，它在两种模式下都可工作。

## 端口模型

| 端口 | 默认绑定 | 用途 |
| --- | --- | --- |
| 80/443 | Server 公网地址 | 主 HTTP/HTTPS edge、ACME、WebUI 与默认 Web 路由 |
| 动态 Web/TCP/UDP 端口 | `TM_EDGE_HTTP_ADDR` 的 host / `TM_TCP_EDGE_BIND_HOST` | v0.4 自定义公网路由 |
| 2222 | 受管 sshd 公网地址 | Agent 主动反向隧道 |
| 8080/8081 | `127.0.0.1` | 管理监听器 / 传统 Gateway |
| 20000–29999 | 回环 | 每路由自动分配的 SSH 转发 |

公网 80 必须到达主 HTTP edge 以完成 HTTP-01。`--http-port 8088` 只改变本机监听，外部仍需做 `80 -> 8088`。HTTP 308 使用 **Server 配置的主 HTTPS 端口**：配置 8443 时 Location 会广告公网 `:8443`。因此只做 `public 443 -> local 8443` 并不能保证 HTTP→HTTPS 跳转可用；还必须让公网 8443 到达该 listener、保持配置端口与公网端口一致，或由能正确重写 Location/接管跳转与 TLS 的前置代理处理。自定义 HTTPS 端口没有这些映射/代理时，URL 必须显式包含端口。

路由的 `Public port` 留空表示主 edge，而不是固定 80/443。主 edge 为 8088/8443 时仍应留空；填写 8443 会被解释为额外 listener 并因占用主端口而拒绝。安装器成功提示会显示实际 WebUI URL 与有效 stream-edge 状态；仍应以 `.env`、`/api/v1/system` 和端到端测试共同验收。

## 固定 v0.4 镜像并验收

```bash
curl -fsSLo install-server.sh https://docs.look4i.com/install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain example.com --version 0.4.1
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml config
docker compose --env-file .env -f compose.yml ps
docker compose --env-file .env -f compose.yml logs --tail=100 server
```

安装器已经启动并验证 HTTPS，不要紧接着用裸 `docker compose up` 绕过保护。保持 Compose 项目名、安装目录和卷路径，不要用特权容器或临时 `docker run` 重建数据库服务。

Server 以非 root 运行；绑定低端口只需 `NET_BIND_SERVICE`。受管 sshd 拒绝 Shell、TTY、X11、Agent forwarding 与用户 RC，仅允许 Ed25519 公钥和回环 `-R`。

## 数据与权限

至少备份：

```text
server-data/portloom.db (+ WAL/SHM when live)
server-data/certs/
ssh-hostkeys/
ssh-auth/
.env
compose.yml
```

`server-data` 与 `ssh-auth` 由 UID/GID 65532 使用。不要用 `0777` 掩盖权限错误，不要只备份单个 SQLite 主文件。

## 上线检查

1. `/api/v1/system` 返回 `0.4.1`，并显示预期的 `tcp_edge`、`udp_edge`、`web_port_edge`；
2. HTTPS 管理入口与 HTTP 308 跳转正常；
3. Agent 安装后无一次性令牌仍能重启和心跳；
4. 分别测试 HTTPS、HTTP、TCP、UDP，以及至少一条路径/自定义端口路由；
5. 核对 Local/Tunnel/Public、近 60 分钟流量和 Server/Agent 资源；
6. 演练数据库、证书缓存和 SSH 主机身份恢复。
