---
layout: home
hero:
  name: PortLoom
  text: 稳定、可靠、快速的自托管隧道代理
  tagline: 把 NAS、家庭服务器和内网服务发布到公网。v0.4 支持 HTTPS、HTTP、TCP、UDP、路径前缀、自定义公网端口，以及流量与资源监控。
  image: { src: /hero-loom.svg, alt: PortLoom 隧道连接示意图 }
  actions:
    - { theme: brand, text: 用 Compose 安装, link: /guide/compose-install }
    - { theme: alt, text: 五分钟快速开始, link: /guide/quick-start }
features:
  - { title: 标准 compose.yml 模板, details: '下载 compose.yml 和环境模板，只修改域名与管理员 Token；也可选择自动化安装脚本。' }
  - { title: 四协议与灵活路由, details: 'WebUI 可创建 HTTPS、HTTP、TCP、UDP 路由；Web 路由支持路径前缀、去前缀和自定义公网端口。' }
  - { title: 状态、流量与资源可见, details: '分别展示 Local、Tunnel、Public 状态；Dashboard 展示近 60 分钟流量与资源，metrics API 另提供每路由计数。' }
---
<HomeFlow />
