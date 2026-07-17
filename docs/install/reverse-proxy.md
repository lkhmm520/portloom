# 反向代理接入

简易安装器不再包含 Caddy：默认由 PortLoom Server 自己监听公网 80/443 并管理证书。本页说明已有 Caddy、Nginx 或 Nginx Proxy Manager 的传统/高级集成。

## 禁用原生入口并配置上游

外部入口占用 80/443 时，不要设置 `TM_EDGE_HTTP_ADDR` 和 `TM_EDGE_HTTPS_ADDR`。PortLoom 提供两个传统 HTTP 上游：

| 上游 | 用途 |
| --- | --- |
| `127.0.0.1:8080` | 管理域名、WebUI 和 API |
| `127.0.0.1:8081` | 所有 HTTP 业务域名共享的 Gateway |

管理域名只转发到 8080。业务域名转发到 8081，并保留原始 Host。TLS 由外部入口终止。

```nginx
location / {
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_pass http://127.0.0.1:8081;
}
```

WebSocket 升级头、上传大小和超时应按业务设置；大文件服务还应验证 Range 请求。桥接网络代理不能通过自己的 `127.0.0.1` 访问宿主回环，请使用 host 网络或受防火墙保护的私网绑定。不要把 8080 直接暴露到公网。

## 外部 Caddy `ask` 兼容

需要保留 on-demand TLS 的外部 Caddy 可以设置 `TM_PUBLIC_HOST`、`TM_TLS_ASK_TOKEN`，并让 Caddy 通过默认回环地址 `127.0.0.1:8082` 调用 `/api/v1/tls/allow`。该端点只授权管理域名和已启用的 HTTP 路由域名。`TM_TLS_ASK_ADDR` 必须是回环 IP；不要公开该端点或 Token。

这是可选兼容模式。原生 HTTPS 入口直接使用 autocert HostPolicy，不需要 `TM_TLS_ASK_*` 或 `/api/v1/tls/allow`。

## 验证

```bash
curl -i -H 'Host: app.example.com' http://127.0.0.1:8081/
curl -I https://app.example.com/
```

Gateway 返回 404 表示没有匹配且已启用的 HTTP 路由；502 表示路由存在，但 Agent 隧道或本地服务不可用。
