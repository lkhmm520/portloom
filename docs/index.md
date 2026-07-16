---
layout: home
hero:
  name: PortLoom
  text: 把反向隧道织成可管理的基础设施
  tagline: 为 NAS 与家庭实验室设计的自托管控制平面。保留现有 NPM、TLS 与 DNS，用受限 OpenSSH 隧道、分层健康状态和可回滚配置发布服务。
  image: { src: /hero-loom.svg, alt: PortLoom 动态隧道编织图 }
  actions:
    - { theme: brand, text: 五分钟快速开始, link: /guide/quick-start }
    - { theme: alt, text: Docker 安装, link: /install/docker }
    - { theme: alt, text: GitHub, link: 'https://github.com/lkhmm520/portloom' }
features:
  - { title: 不替换现有入口, details: 'NPM 继续负责证书与 HTTPS；多个域名共享 Host 路由网关，迁移时旧路径可并行保留。' }
  - { title: 每一层都可观察, details: '分别展示本地服务、SSH 隧道和期望/观测版本，避免“进程还在”被误判为服务可用。' }
  - { title: 默认收紧权限, details: '专用 SSH 账户、固定主机指纹、loopback 转发、非 root 容器、只读根文件系统与最小 Linux 能力。' }
---
<HomeFlow />
