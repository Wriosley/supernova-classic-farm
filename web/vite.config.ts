import { fileURLToPath } from 'node:url'
import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

const repositoryRoot = fileURLToPath(new URL('../', import.meta.url))

function port(value: string | undefined, fallback: number): number {
  if (!value) {
    return fallback
  }
  const parsed = Number(value)
  if (!Number.isInteger(parsed) || parsed < 1 || parsed > 65535) {
    throw new Error(`Invalid port: ${value}`)
  }
  return parsed
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, repositoryRoot, '')
  const webPort = port(env.WEB_PORT, 5173)
  const loginPort = port(env.LOGIN_PORT, 8080)
  const h5Origin = env.H5_ORIGIN || `http://localhost:${webPort}`

  return {
    envDir: repositoryRoot,
    plugins: [vue()],
    server: {
      host: '0.0.0.0',
      port: webPort,
      strictPort: true,
      proxy: {
        '/v1': {
          target: `http://127.0.0.1:${loginPort}`,
          changeOrigin: false,
          headers: {
            Origin: h5Origin,
          },
        },
      },
    },
    preview: {
      host: '0.0.0.0',
      port: webPort,
      strictPort: true,
    },
  }
})
