# 反向代理接入

PortLoom 不接管 TLS。推荐让 NPM、Caddy 或 Nginx 继续作为公网入口。

## 管理域名

例如 `portloom.example.com`：

- Scheme：`http`
- Forward Host：Server 宿主地址
- Forward Port：`8080`
- TLS：启用并强制 HTTPS
- Access List/VPN：强烈建议

## 业务域名

所有 HTTP 业务域名均可转发到同一个 `8081` Gateway。必须保留原始 `Host`。

```nginx
location / {
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_pass http://127.0.0.1:8081;
}
```

如果反向代理在容器中，`127.0.0.1` 指向容器自身。可使用 host network、`host-gateway` 或仅在私网接口监听 PortLoom。

## 验证

```bash
curl -i -H 'Host: app.example.com' http://127.0.0.1:8081/
curl -I https://app.example.com/
```

Gateway 返回 404 通常表示没有匹配的已启用 HTTP 路由；502 表示找到路由但 SSH 回环端口不可用。
