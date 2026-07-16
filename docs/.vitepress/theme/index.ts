import DefaultTheme from 'vitepress/theme'
import type { Theme } from 'vitepress'
import HomeFlow from './components/HomeFlow.vue'
import DownloadCard from './components/DownloadCard.vue'
import './custom.css'
export default { extends: DefaultTheme, enhanceApp({ app }) { app.component('HomeFlow', HomeFlow); app.component('DownloadCard', DownloadCard) } } satisfies Theme
