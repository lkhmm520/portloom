# PortLoom 是什么

PortLoom 是一个自托管的隧道代理。它把家中 NAS 或公司内网里的 Web 服务，通过一台有公网地址的 Docker 主机发布到互联网。

## 需要哪两台主机

| 位置 | 安装内容 | 负责什么 |
| --- | --- | --- |
| 公网 VPS 或云主机 | PortLoom Server | WebUI、管理接口、HTTPS 入口和隧道入口 |
| NAS 或内网服务器 | PortLoom Agent | 主动连接 Server，把流量转发给本地服务 |

Agent 只需要向外访问 Server 的 HTTPS 和 SSH 端口。路由器不需要给 NAS 做端口转发。

## 一次访问怎么走

```text
浏览器
  │ HTTPS
  ▼
公网 Docker 主机
  Caddy → PortLoom Gateway
              │
              │ 已建立的加密反向隧道
              ▼
内网 Docker 主机
  PortLoom Agent → Jellyfin / 博客 / 管理页面
```

隧道由 Agent 主动连接 Server。访问流量则沿已建立的隧道返回内网服务。

## 日常怎么用

安装完成后，日常操作只在 WebUI 里进行：

1. 添加一台 Agent，复制网页生成的安装命令；
2. 在 NAS 上粘贴执行；
3. 新建路由，填写本地地址、端口和公网域名；
4. 查看本地服务和隧道是否在线。

## 当前边界

当前版本完整管理 HTTP/HTTPS 域名路由。TCP 字段仅为兼容元数据；内置公网入口和 WebUI 不创建公网 TCP 监听，也不会把这类记录显示为已发布或健康。Server 使用单个 SQLite 数据库，不提供多Server主动集群。

已有 Caddy、Nginx 或 Nginx Proxy Manager 的用户可以继续使用原入口，参见[反向代理接入](/install/reverse-proxy)。它们是可选集成，不是安装PortLoom的前提。
