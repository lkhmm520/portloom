import { defineConfig, type DefaultTheme } from 'vitepress'

const docsBase = process.env.DOCS_BASE || '/'

const zhSidebar: DefaultTheme.SidebarItem[] = [
  { text: '开始使用', items: [
    { text: '五分钟快速开始', link: '/guide/quick-start' }, { text: 'Compose 模板安装', link: '/guide/compose-install' }, { text: '认识 PortLoom', link: '/guide/what-is-portloom' }, { text: '核心概念', link: '/guide/concepts' }
  ]},
  { text: '安装部署', items: [
    { text: 'Docker 安装', link: '/install/docker' }, { text: '生产环境部署', link: '/install/production' }, { text: '反向代理接入', link: '/install/reverse-proxy' }
  ]},
  { text: '使用指南', items: [
    { text: '客户端与注册', link: '/usage/clients' }, { text: '路由管理', link: '/usage/routes' }, { text: '健康状态', link: '/usage/health' }
  ]},
  { text: '运维', items: [
    { text: '安全加固', link: '/operations/security' }, { text: '备份、升级与回滚', link: '/operations/backup-upgrade' }, { text: '故障排查', link: '/operations/troubleshooting' }, { text: '发布验收', link: '/operations/release-checklist' }
  ]},
  { text: '参考', items: [
    { text: '配置参考', link: '/reference/configuration' }, { text: 'HTTP API', link: '/reference/api' }, { text: '模板下载', link: '/reference/templates' }, { text: '系统架构', link: '/reference/architecture' }
  ]}
]

const enSidebar: DefaultTheme.SidebarItem[] = [
  { text: 'Getting started', items: [
    { text: 'Five-minute quick start', link: '/en/guide/quick-start' }, { text: 'Compose template install', link: '/en/guide/compose-install' }, { text: 'What is PortLoom?', link: '/en/guide/what-is-portloom' }, { text: 'Core concepts', link: '/en/guide/concepts' }
  ]},
  { text: 'Installation', items: [
    { text: 'Install with Docker', link: '/en/install/docker' }, { text: 'Production deployment', link: '/en/install/production' }, { text: 'Reverse proxy integration', link: '/en/install/reverse-proxy' }
  ]},
  { text: 'Guides', items: [
    { text: 'Clients and enrollment', link: '/en/usage/clients' }, { text: 'Route management', link: '/en/usage/routes' }, { text: 'Health model', link: '/en/usage/health' }
  ]},
  { text: 'Operations', items: [
    { text: 'Security hardening', link: '/en/operations/security' }, { text: 'Backup, upgrade, rollback', link: '/en/operations/backup-upgrade' }, { text: 'Troubleshooting', link: '/en/operations/troubleshooting' }, { text: 'Release acceptance', link: '/en/operations/release-checklist' }
  ]},
  { text: 'Reference', items: [
    { text: 'Configuration', link: '/en/reference/configuration' }, { text: 'HTTP API', link: '/en/reference/api' }, { text: 'Templates', link: '/en/reference/templates' }, { text: 'Architecture', link: '/en/reference/architecture' }
  ]}
]

const shared = {
  logo: '/logo.svg', siteTitle: 'PortLoom', externalLinkIcon: true,
  socialLinks: [{ icon: 'github' as const, link: 'https://github.com/lkhmm520/portloom' }]
}

const zhTheme: DefaultTheme.Config = {
  ...shared,
  nav: [
    { text: '开始安装', link: '/guide/quick-start' }, { text: '运维', link: '/operations/security' },
    { text: '参考', link: '/reference/configuration' }
  ],
  sidebar: zhSidebar,
  search: { provider: 'local', options: { translations: {
    button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
    modal: { noResultsText: '没有找到相关结果', resetButtonTitle: '清除查询', backButtonTitle: '关闭搜索', displayDetails: '显示详情', footer: { selectText: '选择', selectKeyAriaLabel: '回车', navigateText: '切换', navigateUpKeyAriaLabel: '向上', navigateDownKeyAriaLabel: '向下', closeText: '关闭', closeKeyAriaLabel: '退出' } }
  }}},
  editLink: { pattern: 'https://github.com/lkhmm520/portloom/edit/main/docs/:path', text: '在 GitHub 上编辑此页' },
  footer: { message: 'PortLoom · 自托管基础设施应当清晰、可验证、可回滚', copyright: 'Copyright © PortLoom contributors' },
  docFooter: { prev: '上一页', next: '下一页' }, outline: { level: [2, 3], label: '本页目录' },
  returnToTopLabel: '返回顶部', sidebarMenuLabel: '文档导航', darkModeSwitchLabel: '外观', langMenuLabel: '切换语言'
}

const enTheme: DefaultTheme.Config = {
  ...shared,
  nav: [
    { text: 'Install', link: '/en/guide/quick-start' }, { text: 'Operations', link: '/en/operations/security' },
    { text: 'Reference', link: '/en/reference/configuration' }
  ],
  sidebar: enSidebar,
  search: { provider: 'local' },
  editLink: { pattern: 'https://github.com/lkhmm520/portloom/edit/main/docs/:path', text: 'Edit this page on GitHub' },
  footer: { message: 'PortLoom · Self-hosted infrastructure should be clear, verifiable, and reversible', copyright: 'Copyright © PortLoom contributors' },
  docFooter: { prev: 'Previous', next: 'Next' }, outline: { level: [2, 3], label: 'On this page' },
  returnToTopLabel: 'Return to top', sidebarMenuLabel: 'Documentation navigation', darkModeSwitchLabel: 'Appearance', langMenuLabel: 'Change language'
}

export default defineConfig({
  title: 'PortLoom', description: '稳定、可靠、快速的自托管隧道代理', lang: 'zh-CN',
  base: docsBase, cleanUrls: true,
  srcExclude: ['WORKPLAN.md', 'architecture.md', 'deployment.md', 'FEEDBACK-BACKLOG.md'],
  lastUpdated: process.env.DOCS_LAST_UPDATED != 'false',
  sitemap: { hostname: process.env.DOCS_ORIGIN || 'https://docs.961121.xyz/' },
  head: [
    ['link', { rel: 'icon', href: `${docsBase}favicon.svg`, type: 'image/svg+xml' }],
    ['meta', { name: 'theme-color', content: '#10b981' }], ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:title', content: 'PortLoom Documentation' }],
    ['meta', { property: 'og:description', content: '稳定、可靠、快速的自托管隧道代理' }]
  ],
  themeConfig: { search: { provider: 'local' } },
  locales: {
    root: { label: '简体中文', lang: 'zh-CN', link: '/', themeConfig: zhTheme },
    en: { label: 'English', lang: 'en-US', link: '/en/', title: 'PortLoom', description: 'A stable, reliable, fast self-hosted tunnel proxy', themeConfig: enTheme }
  }
})
