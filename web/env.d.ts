/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}

/** 后端在 index.html 注入的启动配置（见 internal/web）。 */
interface ImgenBoot {
  version: string
  buildTime: string
  maxUploadMB: number
  concurrency: number
}

declare global {
  interface Window {
    __IMGEN__?: ImgenBoot
  }
}

export {}
