# 发布验收清单

本页面向维护者，不是安装前置步骤。新手安装流程应只引用已经公开并通过本清单的发布物。

## 首次公开发布门槛

首次公开发布完成前：

- `https://docs.961121.xyz/install-server.sh` 与 `install-agent.sh` 不能视为稳定可用的安装入口；
- `ghcr.io/lkhmm520/portloom-sshd:<version>` 未发布前，默认双容器 Server 安装无法完成；
- 不应仅因文档或命令已经合并，就宣称两主机安装可用。

首次发布必须以同一安全版本 Tag 发布 Server、Agent、sshd 和 docs 镜像，部署文档站脚本，再执行下述发布后验收。

## 发布顺序

1. 推送版本 Tag，让 `publish-images` 完成质量门、四个精确版本镜像发布、Commit Revision 校验、双架构检查及 docs 镜像内安装器校验。已存在的精确版本 Tag 不会被覆盖；重跑只补齐缺失镜像。该工作流不等待外部文档站部署，也不会提前移动 `latest`、主版本或次版本通道。
2. 在权威文档主机部署精确的 `ghcr.io/lkhmm520/portloom-docs:<version>`，不要仅依赖浮动 `latest`。
3. 手动运行 `finalize-release` 并填写同一 `v<version>` Tag。它会重新验证两个架构的镜像 Revision、生产文档和两个安装器 SHA-256；稳定版验收完成后才移动 `latest`、主版本和次版本通道，且拒绝用旧版本回放降级通道；预发布版本不会移动这些通道。全部完成后才创建或验证带精确 Commit 标记的 GitHub Release。
4. 如果文档部署或公网校验失败，只需修复部署后重跑 `finalize-release`；不要重复构建和发布已经验证过的镜像。

## 每次发布后的制品验收

1. 从公开文档域名下载两个脚本，确认 HTTP 成功、文件非空且 `sh -n` 通过；
2. 确认 Server、Agent、sshd、docs 四个 GHCR 镜像均存在完整版本 Tag；
3. 使用 `install-server.sh --version <tag>`，确认生成的 Server 与 sshd 镜像使用同一 Tag；
4. 登录 WebUI，确认 `/api/v1/system` 返回该版本，生成的 Agent 命令包含同值 `--version`；
5. 对空白 NAS/内网 Docker 主机执行该命令，确认 Agent 注册后一次性令牌从配置移除；
6. 在 WebUI 新建 HTTP 路由，验证 HTTPS、Host、Local、Tunnel、Public 与重启恢复；
7. 确认 WebUI 不能新建 TCP 路由，已有 TCP 元数据不显示为 published/healthy；
8. 按[备份、升级与回滚](/operations/backup-upgrade)完成一次版本固定升级和回滚演练。

任一关键步骤失败，应停止宣传或升级 `latest`，修复制品后从头复验。
