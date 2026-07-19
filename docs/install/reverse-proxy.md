# 反向代理接入

默认简易安装由 PortLoom 自己管理 80/443。本页只适用于必须保留现有 Caddy、Nginx 或 Nginx Proxy Manager 的高级模式。

## 禁用原生 Web edge

不要设置 `TM_EDGE_HTTP_ADDR`/`TM_EDGE_HTTPS_ADDR`，并提供两个受保护上游：

| 上游 | 用途 |
| --- | --- |
| `127.0.0.1:8080` | 管理域名、WebUI、API、`/healthz` |
| `127.0.0.1:8081` | 业务域名的传统 Web Gateway |

先在 Nginx 的 `http {}` 上下文定义 WebSocket 连接变量（不能放进 `location`）：

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}
```

再在对应 `server {}` 中添加：

```nginx
location / {
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_pass http://127.0.0.1:8081;
}
```

桥接网络中的代理不能用自身 `127.0.0.1` 访问宿主回环；使用 host network 或受防火墙保护的私网 bind。不要把 8080/8081 直接暴露公网。

## v0.4 兼容边界

8081 是 **scheme-agnostic** 传统入口：外部代理负责 HTTP/HTTPS、证书和跳转语义。不要为同一 Host/路径同时创建 HTTP 与 HTTPS 路由，否则传统 Gateway 无法可靠区分。路径前缀仍可匹配。

禁用原生 edge 后：

- `web_port_edge=false`，API 拒绝带自定义 `public_port` 的 Web 路由；
- 管理域名子路径共享和 extra Web listener 应在外部代理中显式配置；
- TCP/UDP stream edge 仍独立工作，除非 `TM_TCP_EDGE_BIND_HOST=off`。

## 外部 Caddy `ask`

可设置 `TM_PUBLIC_HOST`、`TM_TLS_ASK_TOKEN`，让 Caddy 仅通过 `127.0.0.1:8082/api/v1/tls/allow` 查询。该端点授权管理域名与已启用 **HTTPS** 路由域名，不授权 HTTP 路由。`TM_TLS_ASK_ADDR` 必须是回环 IP，端点和 Token 都不能暴露公网。

## 验证

```bash
curl -i -H 'Host: app.example.com' http://127.0.0.1:8081/
curl -I https://app.example.com/
```

404 表示无已启用且已收敛的匹配路由；502 表示路由已匹配，但 Agent 隧道或本地服务不可用。对媒体服务继续验证 WebSocket、Range、上传大小和长连接超时。
