import { create, fromBinary, toBinary } from '@bufbuild/protobuf'
import {
  ClientBootstrapResponseSchema,
  ClientConfigPackageSchema,
  CsrfResponseSchema,
  HttpErrorCode,
  HttpErrorSchema,
  LoginRequestSchema,
  LoginResponseSchema,
  RegisterRequestSchema,
  RegisterResponseSchema,
  WsTicketRequestSchema,
  WsTicketResponseSchema,
  type ClientBootstrapResponse,
  type ClientConfigPackage,
  type CsrfResponse,
  type GatewayEndpoint,
  type SessionView,
  type WsTicketResponse,
} from '../gen/classicfarm/v1/http/http_pb'
import { verifySha256 } from './hash'

const PROTOBUF_MEDIA_TYPE = 'application/x-protobuf'
const MAX_API_BYTES = 16 * 1024
const MAX_CONFIG_BYTES = 2 * 1024 * 1024

export class ProtobufHttpError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code?: HttpErrorCode,
    readonly correlationId?: string,
    readonly retryable = false,
  ) {
    super(message)
    this.name = 'ProtobufHttpError'
  }
}

async function readLimited(response: Response, limit: number): Promise<Uint8Array> {
  const declaredLength = response.headers.get('content-length')
  if (declaredLength !== null && Number(declaredLength) > limit) {
    throw new Error(`响应超过 ${limit} 字节限制`)
  }

  if (!response.body) {
    return new Uint8Array()
  }

  const reader = response.body.getReader()
  const chunks: Uint8Array[] = []
  let length = 0
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) {
        break
      }
      length += value.byteLength
      if (length > limit) {
        await reader.cancel()
        throw new Error(`响应超过 ${limit} 字节限制`)
      }
      chunks.push(value)
    }
  } finally {
    reader.releaseLock()
  }

  const bytes = new Uint8Array(length)
  let offset = 0
  for (const chunk of chunks) {
    bytes.set(chunk, offset)
    offset += chunk.byteLength
  }
  return bytes
}

function assertProtobufResponse(response: Response): void {
  const contentType = response.headers.get('content-type')?.split(';', 1)[0].trim()
  if (contentType !== PROTOBUF_MEDIA_TYPE) {
    throw new Error(`响应 Content-Type 无效：${contentType ?? '缺失'}`)
  }
}

async function throwHttpError(response: Response): Promise<never> {
  let detail = `HTTP ${response.status}`
  let code: HttpErrorCode | undefined
  let correlationId = response.headers.get('x-request-id') ?? undefined
  let retryable = false

  try {
    const bytes = await readLimited(response, MAX_API_BYTES)
    if (bytes.byteLength > 0) {
      const error = fromBinary(HttpErrorSchema, bytes)
      code = error.code
      correlationId = error.correlationId || correlationId
      retryable = error.retryable
      detail = HttpErrorCode[error.code] ?? detail
    }
  } catch {
    // Preserve the status-only error when a proxy or server returns a malformed body.
  }

  throw new ProtobufHttpError(detail, response.status, code, correlationId, retryable)
}

async function request(path: string, init: RequestInit = {}): Promise<Response> {
  const response = await fetch(path, {
    ...init,
    credentials: 'include',
    redirect: 'error',
    headers: {
      Accept: PROTOBUF_MEDIA_TYPE,
      ...init.headers,
    },
  })
  if (!response.ok) {
    await throwHttpError(response)
  }
  assertProtobufResponse(response)
  return response
}

function localConfigUrl(advertisedUrl: string): string {
  const url = new URL(advertisedUrl, window.location.href)
  const isLoopback = url.hostname === '127.0.0.1' || url.hostname === 'localhost'
  if (
    import.meta.env.DEV &&
    isLoopback &&
    url.pathname.startsWith('/v1/')
  ) {
    return `${url.pathname}${url.search}`
  }
  return url.href
}

export async function fetchCsrf(): Promise<CsrfResponse> {
  const response = await request('/v1/auth/csrf')
  return fromBinary(CsrfResponseSchema, new Uint8Array(await response.arrayBuffer()))
}

export async function authenticate(
  mode: 'register' | 'login',
  accountName: string,
  password: string,
  csrfToken: string,
): Promise<SessionView> {
  const requestSchema = mode === 'register' ? RegisterRequestSchema : LoginRequestSchema
  const responseSchema = mode === 'register' ? RegisterResponseSchema : LoginResponseSchema
  const body = toBinary(requestSchema, create(requestSchema, { accountName, password }))
  const response = await request(`/v1/auth/${mode}`, {
    method: 'POST',
    headers: {
      'Content-Type': PROTOBUF_MEDIA_TYPE,
      'X-CSRF-Token': csrfToken,
    },
    body,
  })
  const decoded = fromBinary(
    responseSchema,
    new Uint8Array(await response.arrayBuffer()),
  )
  if (!decoded.session) {
    throw new Error('认证响应缺少 Session')
  }
  return decoded.session
}

export async function fetchBootstrap(): Promise<ClientBootstrapResponse> {
  const response = await request('/v1/bootstrap')
  const bootstrap = fromBinary(
    ClientBootstrapResponseSchema,
    new Uint8Array(await response.arrayBuffer()),
  )
  if (!bootstrap.authBootstrap || bootstrap.gateways.length === 0) {
    throw new Error('bootstrap 缺少认证信息或 Gateway')
  }
  return bootstrap
}

export function selectGateway(gateways: GatewayEndpoint[]): GatewayEndpoint {
  const gateway = [...gateways].sort(
    (left, right) =>
      left.priority - right.priority || left.gatewayId.localeCompare(right.gatewayId),
  )[0]
  if (!gateway?.gatewayId || !gateway.websocketUrl) {
    throw new Error('没有可用的 Gateway')
  }
  return gateway
}

export async function issueWsTicket(
  gatewayId: string,
  csrfToken: string,
  ticketRequestId = crypto.randomUUID(),
): Promise<WsTicketResponse> {
  const body = toBinary(
    WsTicketRequestSchema,
    create(WsTicketRequestSchema, { gatewayId, ticketRequestId }),
  )
  const response = await request('/v1/ws-tickets', {
    method: 'POST',
    headers: {
      'Content-Type': PROTOBUF_MEDIA_TYPE,
      'X-CSRF-Token': csrfToken,
    },
    body,
  })
  const ticket = fromBinary(
    WsTicketResponseSchema,
    new Uint8Array(await response.arrayBuffer()),
  )
  if (!ticket.wsTicket || ticket.gatewayId !== gatewayId) {
    throw new Error('Ticket 响应与所选 Gateway 不匹配')
  }
  return ticket
}

export async function downloadClientConfig(
  advertisedUrl: string,
  expectedDigest: Uint8Array,
  expectedVersion: bigint,
): Promise<ClientConfigPackage> {
  const response = await fetch(localConfigUrl(advertisedUrl), {
    credentials: 'omit',
    headers: { Accept: PROTOBUF_MEDIA_TYPE },
  })
  if (!response.ok) {
    throw new Error(`客户端配置下载失败：HTTP ${response.status}`)
  }
  assertProtobufResponse(response)
  const bytes = await readLimited(response, MAX_CONFIG_BYTES)
  await verifySha256(bytes, expectedDigest)

  const config = fromBinary(ClientConfigPackageSchema, bytes)
  if (config.schemaVersion !== 1) {
    throw new Error(`不支持的客户端配置 schema：${config.schemaVersion}`)
  }
  if (config.clientConfigVersion !== expectedVersion) {
    throw new Error('客户端配置版本与 bootstrap 不一致')
  }
  return config
}
