import { fileURLToPath } from 'node:url'
import type { IncomingMessage, ServerResponse } from 'node:http'
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

const hopByHopHeaders = new Set([
  'connection',
  'keep-alive',
  'proxy-authenticate',
  'proxy-authorization',
  'te',
  'trailer',
  'transfer-encoding',
  'upgrade',
])

async function readBody(request: IncomingMessage): Promise<ArrayBuffer | undefined> {
  if (request.method === 'GET' || request.method === 'HEAD') {
    return undefined
  }
  const chunks: Buffer[] = []
  for await (const chunk of request) {
    chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk))
  }
  const combined = Buffer.concat(chunks)
  const bytes = new Uint8Array(combined.length)
  bytes.set(combined)
  return bytes.buffer
}

async function fetchProxy(
  request: IncomingMessage,
  response: ServerResponse,
  target: string,
  origin: string,
): Promise<number> {
  const headers = new Headers()
  for (const [name, value] of Object.entries(request.headers)) {
    const lowerName = name.toLowerCase()
    if (value === undefined || lowerName === 'host' || lowerName === 'content-length' || hopByHopHeaders.has(lowerName)) {
      continue
    }
    for (const item of Array.isArray(value) ? value : [value]) {
      headers.append(name, item)
    }
  }
  headers.set('origin', origin)
  headers.set('accept-encoding', 'identity')

  const upstream = await fetch(new URL(request.url || '/', target), {
    method: request.method,
    headers,
    body: await readBody(request),
    redirect: 'manual',
  })
  const body = Buffer.from(await upstream.arrayBuffer())
  response.statusCode = upstream.status
  response.statusMessage = upstream.statusText

  for (const [name, value] of upstream.headers) {
    const lowerName = name.toLowerCase()
    if (lowerName === 'set-cookie' || lowerName === 'content-length' || hopByHopHeaders.has(lowerName)) {
      continue
    }
    response.setHeader(name, value)
  }
  const setCookies = (
    upstream.headers as Headers & { getSetCookie?: () => string[] }
  ).getSetCookie?.()
  if (setCookies?.length) {
    response.setHeader('set-cookie', setCookies)
  }
  response.setHeader('content-length', String(body.length))
  response.end(body)
  return upstream.status
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, repositoryRoot, '')
  const webPort = port(env.WEB_PORT, 5173)
  const loginPort = port(env.LOGIN_PORT, 8080)
  const gatePort = port(env.GATE_PORT, 8081)
  const h5Origin = env.H5_ORIGIN || `http://localhost:${webPort}`
  const loginProxyTarget = env.LOGIN_PROXY_TARGET || `http://127.0.0.1:${loginPort}`
  const gateProxyTarget = env.GATE_PROXY_TARGET || `ws://127.0.0.1:${gatePort}`

  return {
    envDir: repositoryRoot,
    plugins: [
      vue(),
      {
        name: 'classic-farm-dev-api-fetch-proxy',
        configureServer(server) {
          server.middlewares.use((request, response, next) => {
            if (!request.url?.startsWith('/v1/')) {
              next()
              return
            }
            const hasCSRFCookie = request.headers.cookie?.includes('cf_csrf_dev=') ?? false
            const hasCSRFHeader = typeof request.headers['x-csrf-token'] === 'string'
            const fetchSite = request.headers['sec-fetch-site'] || 'absent'
            void fetchProxy(request, response, loginProxyTarget, h5Origin)
              .then((status) => {
                server.config.logger.info(
                  `[api-fetch] ${request.method} ${request.url} status=${status} csrf_cookie=${hasCSRFCookie} csrf_header=${hasCSRFHeader} fetch_site=${fetchSite}`,
                )
              })
              .catch((error: unknown) => {
                server.config.logger.error(`fetch proxy error: ${String(error)}`)
                if (!response.headersSent) {
                  response.statusCode = 502
                  response.setHeader('content-type', 'text/plain; charset=utf-8')
                }
                response.end('Bad Gateway')
              })
          })
        },
      },
    ],
    server: {
      host: '0.0.0.0',
      port: webPort,
      strictPort: true,
      proxy: {
        // Gate binds and is advertised loopback-only, so a browser on another
        // host reaches it through this proxy instead of dialing it directly.
        '/ws': {
          target: gateProxyTarget,
          ws: true,
          changeOrigin: true,
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
