---
layout: home
hero:
  name: PortLoom
  text: 稳定、可靠、快速的自托管隧道代理
  tagline: 把 NAS、家庭服务器和内网服务发布到公网。v0.4 支持 HTTPS、HTTP、TCP、UDP、路径前缀、自定义公网端口，以及流量与资源监控。
  image: { src: /hero-loom.svg, alt: PortLoom 隧道连接示意图 }
  actions:
    - { theme: brand, text: 开始安装, link: /guide/quick-start }
    - { theme: alt, text: 查看 v0.4 路由能力, link: /usage/routes }
features:
  - { title: 两台主机，一条 Agent 命令, details: '公网主机运行 Server 与受管 sshd；NAS 只运行 Agent。密钥、主机指纹和一次性令牌由安装流程自动处理。' }
  - { title: 四协议与灵活路由, details: 'WebUI 可创建 HTTPS、HTTP、TCP、UDP 路由；Web 路由支持路径前缀、去前缀和自定义公网端口。' }
  - { title: 状态、流量与资源可见, details: '分别展示 Local、Tunnel、Public 状态；Dashboard 展示近 60 分钟流量与资源，metrics API 另提供每路由计数。' }
---
<HomeFlow />
