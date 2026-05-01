// @{"req": ["REQ-FEAUTH-001", "REQ-FECOURSE-001", "REQ-FECONTENT-001", "REQ-FEADMIN-001"]}
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'

const app = createApp(App)

app.use(createPinia())
app.use(router)

app.mount('#app')
