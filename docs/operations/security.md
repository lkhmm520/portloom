# 安全加固

## 最小权限清单

- Server 管理口只在回环/私网监听并置于 HTTPS 后；
- 使用至少 32 字节随机管理员 Token；
- SSH 使用专用非管理员账户与独立密钥；
- `GatewayPorts no`、`AllowTcpForwarding remote`、禁用 TTY/Agent/X11；
- 私钥 0600、`known_hosts` 指纹人工核对；
- 长期运行的 Server 以 UID 65532 非 root 运行；受管 sshd 的 PID 1 与监听进程以 UID 0 运行，以便按连接降权。两者均使用只读根且不挂载 Docker socket；默认安装先 `cap_drop: ALL`，再仅为 Server 回加 `NET_BIND_SERVICE`，为 sshd 回加 `SETUID`、`SETGID`、`SYS_CHROOT`。安装器只在初始化数据目录权限时短暂运行额外的 UID 0 helper，该 helper 不是常驻服务；
- 注册令牌短期、单次使用，注册后立即从环境删除；
- 未执行的 Agent 安装命令及时在令牌列表删除/撤销；
- TCP/UDP 与自定义 Web 端口只在云防火墙放行实际使用的端口；首次安装且不需要 stream edge 时使用 `--disable-tcp-edge`。已有 `.env` 的非空 bind 值会被安装器保留，不能把重跑参数当成通用开关；变更后用 `/api/v1/system` 和真实监听端口共同验证；
- 数据、密钥与备份目录权限 0700/0600。

## 信任边界

管理员 Token 是凭据，不是加密；Agent Token 也是长期凭据。不要通过明文 HTTP 发送。`TM_ALLOW_INSECURE_HTTP` 只允许本机 loopback 开发地址。

HTTP 路由是显式选择的明文发布能力，不应用来承载管理 API 或其他凭据。UDP 经 TCP 型 SSH 隧道封装，不提供额外的应用层认证；公开前仍需由真实服务实施认证、访问控制和速率限制。

## 依赖与文档构建

文档站使用 VitePress 静态构建。Node/Vite 仅存在于构建阶段，最终 `portloom-docs` 镜像只包含非 root Nginx 与静态文件。生产镜像应做镜像扫描并固定版本标签。
