import { createApp } from 'vue'
import App from './App.vue'
import './style.css'
import { reportFatalError } from './lib/fatal-error'

const app = createApp(App)

// A throw inside a render or a watcher leaves Vue unable to patch: the page
// keeps the last painted frame and every click silently does nothing. Without
// this handler that failure looks identical to "the buttons are broken".
app.config.errorHandler = (error, _instance, info) => {
  console.error(`[fatal] Vue ${info}`, error)
  reportFatalError(error, info)
}

window.addEventListener('unhandledrejection', (event) => {
  console.error('[fatal] unhandled rejection', event.reason)
})

app.mount('#app')
