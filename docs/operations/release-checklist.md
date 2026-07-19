# 发布验收清单

本页面向维护者。v0.4 发布不能只验证旧版 HTTPS 域名路由。

## 发布顺序

1. 推送精确版本 Tag，等待 `publish-images` 完成质量门并发布 Server、Agent、sshd、docs 的双架构精确版本镜像；
2. 在权威文档主机部署相同精确版本的 docs 镜像；
3. 运行 `finalize-release`（或项目约定的 `finalize-vX.Y.Z` marker），重新验证镜像 Revision、线上文档和安装器 SHA-256；
4. 只有全部验收后才移动 `latest`、主/次版本通道并发布 GitHub Release。失败只修复阻断并重跑 finalize，不覆盖已存在精确版本制品。

## 制品与冷安装

先将 `VERSION` 设为待发布的精确 v0.4 版本（例如 `0.4.1`）；下列所有镜像、安装命令和运行时版本必须使用同一个值。

- [ ] 两个线上安装脚本 HTTP 200、非空、`bash -n` 通过；
- [ ] 四个 `$VERSION` 镜像存在、双架构 Revision 与 Tag commit 一致；
- [ ] `install-server.sh --version "$VERSION"` 生成相同版本 Server/sshd，并通过真实 HTTPS `/healthz`；
- [ ] `/api/v1/system` 返回 `$VERSION`，`tcp_edge=true`、`udp_edge=true`、`web_port_edge=true`（默认安装）；
- [ ] WebUI 生成的 Agent 命令包含同版本，空白 NAS 安装后 `.env` 不再含一次性令牌；
- [ ] 至少在一套 Synology/QNAP 类环境验证 PATH、Docker daemon、Compose v2 fallback 和无 `flock` Agent 安装。

## v0.4 功能验收

- [ ] HTTPS 默认域名路由：证书、HTTP 308、Host、WebSocket/Range；
- [ ] 真 HTTP 路由：无证书申请、无强制跳转；
- [ ] 同域名路径：最长前缀与 Strip path；
- [ ] 自定义 HTTP/HTTPS 公网端口：listener、scheme 冲突与 bind_error 状态；
- [ ] 管理域名安全子路径：允许普通 HTTPS 前缀，拒绝根、HTTP、自定义端口、`/api|/assets|/healthz`；
- [ ] TCP：公网字节转发、端口冲突、禁用 stream edge；
- [ ] UDP：双向数据报、不同来源会话、60 秒回收、TCP 封装边界；
- [ ] Dashboard：60 分钟曲线、总量、Server/Agent CPU/RSS、Server 重启归零；metrics API：每路由计数；
- [ ] Add Agent 令牌删除：未使用命令失效，已注册 Agent 不受影响。

## 升级与回滚

- [ ] 用 v0.3.x 数据库升级到 v0.4.0，确认 legacy `http` 迁移为 `https` 且旧公网行为不变；
- [ ] 升级前已保存一致的 `server-data`，因为配置备份不含数据库；
- [ ] Server 新版本失败时自动恢复旧配置/镜像 ID；
- [ ] 用升级前数据库完成一次真实 v0.3.x 回滚；
- [ ] 文档明确 Agent 安装器不支持跨版本原地升级，并实际验证顺序维护窗口流程：旧 Agent `down`、安装新 Client、删除并重建路由、端到端验收后保留旧目录与镜像 ID 供回滚。

任一关键项失败，应阻止 finalize 与通道移动，不得通过手改线上实例掩盖制品问题。
