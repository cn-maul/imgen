import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'

import './styles/tokens.css'
import './styles/base.css'
import './styles/atomic.css'
import './styles/components/index.css'

createApp(App).use(createPinia()).use(router).mount('#app')
