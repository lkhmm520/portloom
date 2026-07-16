# 安全加固

## 最小权限清单

- Server 管理口只在回环/私网监听并置于 HTTPS 后；
- 使用至少 32 字节随机管理员 Token；
- SSH 使用专用非管理员账户与独立密钥；
- `GatewayPorts no`、`AllowTcpForwarding remote`、禁用 TTY/Agent/X11；
- 私钥 0600、`known_hosts` 指纹人工核对；
- 容器非 root、只读根、`cap_drop: ALL`、无 Docker socket；
- 注册令牌短期、单次使用，注册后立即从环境删除；
- 数据、密钥与备份目录权限 0700/0600。

## 信任边界

管理员 Token 是凭据，不是加密；Agent Token 也是长期凭据。不要通过明文 HTTP 发送。`TM_ALLOW_INSECURE_HTTP` 只允许本机 loopback 开发地址。

## 依赖与文档构建

文档站使用 VitePress 静态构建。Node/Vite 仅存在于构建阶段，最终 `portloom-docs` 镜像只包含非 root Nginx 与静态文件。生产镜像应做镜像扫描并固定版本标签。
