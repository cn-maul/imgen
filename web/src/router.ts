import { createRouter, createWebHashHistory } from 'vue-router'

export const router = createRouter({
  // hash 模式：单二进制直接 serve 静态产物，无需服务端重写兜底之外的东西。
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/studio' },
    {
      path: '/studio',
      name: 'studio',
      component: () => import('@/views/StudioView.vue'),
      meta: { title: '工作台' },
    },
    {
      path: '/presets',
      name: 'presets',
      component: () => import('@/views/PresetsView.vue'),
      meta: { title: '提示词库' },
    },
    {
      path: '/assets',
      name: 'assets',
      component: () => import('@/views/AssetsView.vue'),
      meta: { title: '资产库' },
    },
    {
      path: '/history',
      name: 'history',
      component: () => import('@/views/HistoryView.vue'),
      meta: { title: '生成历史' },
    },
    {
      path: '/settings/providers',
      name: 'settings-providers',
      component: () => import('@/views/ProvidersView.vue'),
      meta: { title: '通道配置' },
    },
    {
      path: '/settings/system',
      name: 'settings-system',
      component: () => import('@/views/SystemView.vue'),
      meta: { title: '系统设置' },
    },
  ],
})

router.afterEach((to) => {
  const title = (to.meta.title as string | undefined) ?? ''
  document.title = title ? `${title} · Imgen` : 'Imgen'
})
