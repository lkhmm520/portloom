# 五分钟快速开始

本页使用已发布的 GHCR 镜像。完整生产部署请继续阅读[生产环境部署](/install/production)。

## 1. 下载 Server 模板

```bash
mkdir -p portloom/server && cd portloom/server
curl -LO https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/docker-compose.server.yml
curl -Lo server.env https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/server.env.example
mkdir -p data/server
sudo chown -R 65532:65532 data/server
chmod 700 data/server
openssl rand -hex 32
```

把随机值写入 `server.env` 的 `TM_ADMIN_TOKEN`，然后启动：

```bash
docker compose --env-file server.env -f docker-compose.server.yml config
docker compose --env-file server.env -f docker-compose.server.yml up -d
curl --fail http://127.0.0.1:8080/healthz
```

## 2. 配置 HTTPS 与受限 SSH

在 NPM 中把管理域名转发到 Server `8080`，业务域名统一转发到 `8081` 并保留 `Host`。VPS 上的 `tunnel` 账户必须使用 `/usr/sbin/nologin`，禁止命令与 Shell，只允许绑定回环地址的远程转发；完整配置见[生产环境部署](/install/production)。

## 3. 准备并注册 Agent

在 NAS 下载模板并**先生成实际密钥和 `known_hosts`**：

```bash
mkdir -p portloom/agent/{data/agent,secrets} && cd portloom/agent
curl -LO https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/docker-compose.agent.yml
curl -Lo agent.env https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/agent.env.example
ssh-keygen -t ed25519 -a 64 -N '' -f secrets/id_ed25519 -C portloom-agent
ssh-keyscan -p 22 tunnel.example.com > secrets/known_hosts
sudo chown -R 65532:65532 data secrets
chmod 700 data/agent secrets
chmod 600 secrets/id_ed25519
chmod 644 secrets/known_hosts
```

通过可信渠道核对 `known_hosts` 指纹，并把 `secrets/id_ed25519.pub` 加到 VPS `tunnel` 用户的 `authorized_keys`，使用[限制模板](/reference/templates)。登录控制台创建一次性令牌，填写 `agent.env` 后启动：

```bash
docker compose --env-file agent.env -f docker-compose.agent.yml config
docker compose --env-file agent.env -f docker-compose.agent.yml up -d
docker compose -f docker-compose.agent.yml logs --tail=100 agent
```

注册成功后，从 `agent.env` 删除 `TM_ENROLLMENT_TOKEN` 并重新创建容器。

## 4. 创建第一条路由

在控制台选择客户端，填写域名、NAS 本地地址与端口，启用路由。依次验证：本地层为 `up`、隧道层为 `up`、期望与观测版本一致，然后访问公网 HTTPS 地址。
