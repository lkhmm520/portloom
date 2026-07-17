# 生产环境部署

快速开始适合一台空闲VPS。生产部署前先决定公网入口：

- VPS的80/443空闲：使用简易安装器自带的Caddy；
- 已有Caddy、Nginx或NPM：只部署Server与受管sshd，把现有入口转发到PortLoom；
- 有合规要求：固定镜像版本，逐项审计Compose和卷权限。

## 端口与数据流

| 端口 | 默认监听 | 用途 |
| --- | --- | --- |
| 80/443 | Caddy公网地址 | WebUI和HTTP业务域名 |
| 2222 | 受管sshd公网地址 | Agent主动建立反向隧道 |
| 8080 | 127.0.0.1 | Server WebUI和API |
| 8081 | 127.0.0.1 | Host路由Gateway |
| 20000–29999 | 127.0.0.1 | 自动分配的SSH回环端口 |

Agent所在网络只需要出站访问Server的443和2222。

## 固定镜像版本

下载发布版安装脚本并使用`--version`：

```bash
./install-server.sh --domain portloom.example.com --version 0.2.0
```

部署前执行：

```bash
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml config
docker compose up -d
docker compose ps
```

不要使用临时`docker run`重建数据库容器。升级时保留Compose项目名和卷路径。

## 受管SSH边界

`portloom-sshd`是独立容器，不修改宿主机`/etc/ssh/sshd_config`。它只允许：

- Ed25519公钥认证；
- `ssh -N -R`远程转发；
- 绑定`127.0.0.1:*`；
- 无Shell、无TTY、无X11、无Agent转发、无用户RC。

Server对`ssh-auth`卷有写权限，sshd只读。sshd对`ssh-hostkeys`卷有写权限，Server只读。Agent不能修改自己的授权记录。Server启动时从SQLite重建`authorized_keys`，文件丢失不会永久锁死现有Agent。

主机私钥保存在`ssh-hostkeys/ssh_host_ed25519_key`。更换它会触发Agent的严格主机身份校验并中止连接。恢复时应还原原卷，而不是关闭`StrictHostKeyChecking`。

## 数据和权限

至少备份：

```text
server-data/portloom.db
ssh-hostkeys/
.env
Caddyfile
caddy-data/
```

`server-data`和`ssh-auth`由UID/GID 65532写入。简易安装器会通过一次性容器初始化权限。不要把目录改成`0777`。

## 已有公网入口

删除简易Compose中的Caddy服务，保持Server监听回环或受防火墙保护的私网地址。管理域名转发到8080，业务域名转发到8081并保留Host，详见[反向代理接入](/install/reverse-proxy)。受管sshd仍可使用2222，不需要复用宿主22端口。

## 上线验证

1. WebUI可以通过HTTPS登录；
2. 普通SSH命令登录2222被拒绝；
3. Agent显示在线；
4. 本地服务状态为up；
5. 隧道状态为up；
6. 公网域名可访问并保留正确Host；
7. 重启Agent、Server和sshd后路由会自动恢复；
8. 备份可在隔离目录恢复。
