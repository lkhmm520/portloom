# 生产环境部署

## 1. Server 数据目录

```bash
sudo install -d -o 65532 -g 65532 -m 0700 /opt/portloom/server
openssl rand -hex 32
```

把随机值放入 `TM_ADMIN_TOKEN`。管理与网关端口默认只监听回环地址；如果 NPM 在容器内，请使用经过防火墙保护的宿主私网地址，而不是直接开放公网。

## 2. 专用 SSH 账户

创建无管理权限、无登录 Shell 的 `tunnel` 用户，只安装 Agent 公钥：

```bash
sudo useradd --system --create-home --shell /usr/sbin/nologin tunnel
sudo install -d -o tunnel -g tunnel -m 0700 /home/tunnel/.ssh
```

每行 `authorized_keys` 使用以下前缀，限制命令、TTY、用户RC和监听地址：

```text
command="/usr/sbin/nologin",no-agent-forwarding,no-X11-forwarding,no-pty,no-user-rc,permitlisten="127.0.0.1:*" ssh-ed25519 AAAA... portloom-agent
```

在 `sshd_config` 中加入仓库提供的限制片段：

```text
Match User tunnel
    AuthenticationMethods publickey
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    AllowTcpForwarding remote
    GatewayPorts no
    PermitListen 127.0.0.1:*
    PermitTTY no
    X11Forwarding no
    AllowAgentForwarding no
    PermitUserRC no
    ForceCommand /usr/sbin/nologin
```

运行 `sshd -t` 后再 reload。验证普通命令/交互登录被拒绝，同时 Agent 的 `ssh -N -R 127.0.0.1:端口:目标`仍能建立。不要使用会关闭全部转发的`DisableForwarding yes`。

## 3. 密钥与 known_hosts

```bash
sudo install -d -o 65532 -g 65532 -m 0700 /opt/portloom/secrets
sudo -u '#65532' ssh-keygen -t ed25519 -a 64 -N '' -f /opt/portloom/secrets/id_ed25519 -C portloom-agent
ssh-keyscan -p 22 tunnel.example.com | sudo tee /opt/portloom/secrets/known_hosts
sudo chown 65532:65532 /opt/portloom/secrets/*
sudo chmod 600 /opt/portloom/secrets/id_ed25519
sudo chmod 644 /opt/portloom/secrets/known_hosts
```

通过可信渠道核对主机指纹。某些 NAS 即使 `stat` 所有者正确，非 root 容器仍可能无法读取 bind mount；务必使用实际 Agent UID 做一次 `open()` 测试，必要时改用由容器初始化的命名卷。

## 4. 安全迁移

新旧链路并行建立，先用 `curl -H 'Host: app.example.com' http://127.0.0.1:8081/` 验证 Gateway，再逐域名切换 NPM。每次切换记录旧上游，验证状态码、响应哈希和 Range 请求；稳定后只停止旧容器，不立即删除镜像与密钥。
