# 系统架构

```text
                           公网Docker主机
浏览器 ──HTTPS──> Caddy/现有入口 ──Host──> Gateway :8081
                         │                   │
                         │ 管理域名          ▼
                         └──────────> Server / SQLite
                                              │ 写入授权卷
                                              ▼
                                         managed sshd :2222
                                              ▲
                                              │ Agent主动建立OpenSSH -R
                                              │
内网服务 <────────────── Agent <──────────────┘
```

## 管理路径

Server的SQLite保存Agent、令牌校验值、SSH公钥、路由、分配端口和状态。WebUI调用管理API；Agent通过HTTPS注册、拉取路由和发送心跳。当前设计只有一个Server写入者。

## 隧道路径

每个Agent维持一个OpenSSH ControlMaster。路由变更时，Agent使用`ssh -O forward/cancel -R`动态增删反向转发，不需要重启主连接。sshd只允许回环远程转发。

## 公网请求路径

入口终止TLS并保留Host。Gateway按Host选择已启用且已收敛的HTTP路由，再代理到VPS回环端口。OpenSSH把连接带回Agent，Agent连接路由配置的本地目标。

简易安装的Caddy使用受随机Token保护的本地`ask`端点，只为管理域名和WebUI中已启用的HTTP路由申请证书。未知域名不会触发证书签发。

## 故障行为

- Server短暂不可用时，运行中的Agent保留已有转发并重试；
- Agent重启后从Server恢复期望路由；
- SSH主连接失效时，Agent重建主连接和转发；
- 主机公钥变化时，Agent严格拒绝连接；
- Server启动时从SQLite重建Agent授权文件；
- 取消转发失败时不伪报版本收敛；
- 本地服务不可用时，隧道可能仍存在，但Local状态保持down。
