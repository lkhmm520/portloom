# 反向代理接入

简易安装器已经包含Caddy。本页只适用于公网主机已有Caddy、Nginx或Nginx Proxy Manager，或需要把HTTPS交给其他入口的部署。

## 上游

PortLoom有两个HTTP上游：

| 上游 | 用途 |
| --- | --- |
| `127.0.0.1:8080` | 管理域名、WebUI和API |
| `127.0.0.1:8081` | 所有HTTP业务域名共享的Gateway |

管理域名必须只转发到8080。业务域名转发到8081，并保留原始Host。

## Nginx示例

```nginx
location / {
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_pass http://127.0.0.1:8081;
}
```

WebSocket升级头、上传大小和超时应按业务服务设置。Jellyfin等大文件服务还应验证Range请求。

如果反向代理运行在桥接网络容器中，容器里的`127.0.0.1`不是宿主机。可以让入口使用host网络，或把PortLoom监听到受防火墙保护的私网地址。不要为了容器互通把8080直接暴露到公网。

## 验证

```bash
curl -i -H 'Host: app.example.com' http://127.0.0.1:8081/
curl -I https://app.example.com/
```

Gateway返回404表示没有匹配且已启用的HTTP路由；502表示路由存在，但Agent隧道或本地服务不可用。
