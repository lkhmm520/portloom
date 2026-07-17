# 发布验收清单

本页面向维护者，不是安装前置步骤。新手安装流程应只引用已经公开并通过本清单的发布物。

## 首次公开发布门槛

首次公开发布完成前：

- `https://docs.961121.xyz/install-server.sh` 与 `install-agent.sh` 不能视为稳定可用的安装入口；
- `ghcr.io/lkhmm520/portloom-sshd:<version>` 未发布前，默认双容器 Server 安装无法完成；
- 不应仅因文档或命令已经合并，就宣称两主机安装可用。

首次发布必须以同一安全版本 Tag 发布 Server、Agent、sshd 和 docs 镜像，部署文档站脚本，再执行下述发布后验收。

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
