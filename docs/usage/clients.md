# 客户端与注册

## 创建注册令牌

登录控制台，进入“注册令牌”，选择有效期。令牌最长 30 天、只能使用一次，明文只显示一次；Server 只保存校验值。

## 首次启动

Agent 使用 `TM_CLIENT_NAME` 与 `TM_ENROLLMENT_TOKEN` 调用注册接口，获得 Client ID 和长期 Agent Token，并写入 `/data/agent.json`。

注册完成后：

1. 确认 Client 在线且已有心跳；
2. 备份 Agent 状态目录；
3. 从环境文件删除一次性令牌；
4. 重建 Agent，确认仍能心跳。

## 多 Agent 隔离

Web 与媒体流量需要独立 SSH 主连接时，使用两个 Client、两个状态目录和两个注册令牌。它们可以只读共享同一 SSH 私钥和 `known_hosts`，但不能共享 Agent 状态文件。
