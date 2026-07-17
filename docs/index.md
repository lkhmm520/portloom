---
layout: home
hero:
  name: PortLoom
  text: 稳定、可靠、快速的隧道代理
  tagline: 把 NAS、家庭服务器和内网 Web 服务发布到公网。公网主机运行 Server，内网主机运行 Agent；Agent 主动建立加密隧道，内网无需开放入站端口。
  image: { src: /hero-loom.svg, alt: PortLoom 隧道连接示意图 }
  actions:
    - { theme: brand, text: 开始安装, link: /guide/quick-start }
    - { theme: alt, text: 它如何工作, link: /guide/what-is-portloom }
features:
  - { title: 两台主机，一条安装命令, details: '先在公网 Docker 主机安装 Server，再从 WebUI 复制一条命令到 NAS。密钥、主机指纹和注册令牌由安装流程自动处理。' }
  - { title: 路由都在 WebUI 配置, details: '选择 Agent，填写内网地址、端口和公网域名。新增或修改服务不需要再编辑 SSH 命令。' }
  - { title: 安全默认值已经配好, details: 'Agent 主动出站连接；SSH 账户没有 Shell，只允许回环反向转发。容器不挂载 Docker socket。' }
---
<HomeFlow />
