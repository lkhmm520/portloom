# PortLoom v0.5.0 发布说明

候选版使用 Git Tag `v0.5.0`；Server、Agent、托管 sshd 和文档镜像的精确 Tag 均为 `0.5.0`。固定版本不代表制品已经发布。

## 主要变更

- `portloomctl plan` 只比较两台 Server 的路由清单，不修改任一端。Linux 上的 `check`、`status`、`switch`、`settle` 和 `rollback` 使用独立的操作配置及持久化本地日志，不读取 `plan` 报告。`check` 只校验本地输入，不证明线上就绪。操作中断后先查看 `status`；`settle` 只处理待确认的步骤。非零退出不代表没有发生写入。
- 发布闸门让维护者按完整业务主机名冻结或阻断发布，并在切换期间保护对应的路由集合。须在两台 Server 上显式启用闸门接口，并为每个 Gateway 配置不同的 `TM_SERVING_NODE_ID`。参见[配置参考](../reference/configuration.md)。
- GitHub Release 计划包含 `portloomctl-linux-amd64`、`portloomctl-linux-arm64` 和 `SHA256SUMS`。下载后请对照清单校验相应架构的二进制文件。

## 支持的切换范围

切换仅可通过 CLI 操作。它仅支持一个直接提供权威解析的 RFC 2136 DNS 区域内、已预先配置的精确业务主机名，且业务须通过公网 443 端口提供 HTTPS。两端的 Server/Agent、匹配的路由清单、可信 TLS、受保护的预览入口，以及每条映射的应用探针都须事先就绪。CLI 协调发布状态和获准修改的 A/AAAA 记录，不负责创建上述前置条件。发布后可查阅对应 Tag 下的 [CLI 操作指南](https://github.com/lkhmm520/portloom/blob/v0.5.0/cmd/portloomctl/README.md)。

`switch` 不支持迁移生产 Cloudflare 托管 DNS 或代理记录、原始 SSH、TCP/UDP 路由或 Agent。生产 Cloudflare 迁移须另行手动执行。DNS 缓存滞后可能造成中断，切换不会等待正在处理的请求结束。隔离环境的验收不能证明生产 DNS 传播或线上就绪。

## 升级与恢复

升级前停止旧 Server，并为数据做一致性备份。首次成功变更闸门时会写入持久的数据库降级保护，即使之后释放全部闸门也不会移除。不要让 v0.4.6 使用带闸门的数据库，也不要清除保护来强制降级。恢复时须使用已知正常、支持发布闸门的 Server 镜像和完整数据；重新开放入口前核对两端的发布归属。详见[备份、升级与回滚](./backup-upgrade.md)。