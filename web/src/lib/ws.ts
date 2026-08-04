import { create, fromBinary, toBinary } from '@bufbuild/protobuf'
import {
  Action,
  MessageKind,
  WsEnvelopeSchema,
  type AuthResponse,
  type WsEnvelope,
} from '../gen/classicfarm/v1/ws/ws_pb'

const PROTOCOL_VERSION = 1
const MAX_MESSAGE_BYTES = 64 * 1024
const REQUEST_TIMEOUT_MS = 10_000

type PendingRequest = {
  action: Action
  resolve: (envelope: WsEnvelope) => void
  reject: (error: Error) => void
  timer: ReturnType<typeof setTimeout>
}

export type PlayerStateChangedHandler = (envelope: WsEnvelope) => void

export type AuthenticatedConnection = {
  auth: AuthResponse
  requestId: string
  serverTimeMs: bigint
}

function errorMessage(envelope: WsEnvelope): string {
  if (!envelope.error) {
    return '未知 WebSocket 错误'
  }
  return `WebSocket 错误 ${envelope.error.code}`
}

function validateWebSocketUrl(urlText: string): string {
  const url = new URL(urlText)
  if (url.protocol !== 'ws:' && url.protocol !== 'wss:') {
    throw new Error(`Gateway URL 协议无效：${url.protocol}`)
  }
  return url.href
}

export class FarmWebSocket {
  private socket?: WebSocket
  private pending = new Map<string, PendingRequest>()
  private playerStateChangedHandler?: PlayerStateChangedHandler

  get connected(): boolean {
    return this.socket?.readyState === WebSocket.OPEN
  }

  setPlayerStateChangedHandler(handler?: PlayerStateChangedHandler): void {
    this.playerStateChangedHandler = handler
  }

  async connectAndAuth(
    websocketUrl: string,
    wsTicket: string,
    expectedPlayerId: bigint,
    onSocketOpen?: () => void,
  ): Promise<AuthenticatedConnection> {
    this.disconnect()
    const socket = new WebSocket(validateWebSocketUrl(websocketUrl))
    socket.binaryType = 'arraybuffer'
    this.socket = socket
    socket.addEventListener('message', (event) => this.handleMessage(event))
    socket.addEventListener('close', () => {
      if (this.socket === socket) {
        this.rejectAll(new Error('WebSocket 已断开'))
      }
    })
    socket.addEventListener('error', () => {
      if (this.socket === socket && socket.readyState !== WebSocket.OPEN) {
        this.rejectAll(new Error('WebSocket 连接失败'))
      }
    })

    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('WebSocket 连接超时')), REQUEST_TIMEOUT_MS)
      socket.addEventListener(
        'open',
        () => {
          clearTimeout(timer)
          resolve()
        },
        { once: true },
      )
      socket.addEventListener(
        'close',
        (event) => {
          clearTimeout(timer)
          reject(new Error(`WebSocket 在认证前关闭（${event.code}）`))
        },
        { once: true },
      )
    })

    onSocketOpen?.()
    const requestId = crypto.randomUUID()
    const envelope = await this.sendRequest(
      create(WsEnvelopeSchema, {
        protocolVersion: PROTOCOL_VERSION,
        messageKind: MessageKind.REQUEST,
        action: Action.AUTH,
        requestId,
        payload: {
          case: 'authRequest',
          value: { wsTicket },
        },
      }),
    )
    if (envelope.error) {
      throw new Error(errorMessage(envelope))
    }
    if (envelope.payload.case !== 'authResponse') {
      throw new Error('AUTH 响应 payload 无效')
    }

    const auth = envelope.payload.value
    if (
      auth.playerId !== expectedPlayerId ||
      auth.playerId === 0n ||
      auth.heartbeatIntervalMs === 0 ||
      auth.clientConfigVersion === 0n ||
      !auth.clientConfigUrl ||
      auth.clientConfigSha256.byteLength !== 32 ||
      auth.protocolMin > PROTOCOL_VERSION ||
      auth.protocolMax < PROTOCOL_VERSION
    ) {
      throw new Error('AUTH 响应字段校验失败')
    }
    return { auth, requestId, serverTimeMs: envelope.serverTimeMs }
  }

  async requestPlayerSnapshot(playerId: bigint): Promise<WsEnvelope> {
    if (playerId === 0n) {
      throw new Error('authenticated player_id 不能为 0')
    }
    return this.sendRequest(
      create(WsEnvelopeSchema, {
        protocolVersion: PROTOCOL_VERSION,
        messageKind: MessageKind.REQUEST,
        action: Action.GET_PLAYER_SNAPSHOT,
        requestId: crypto.randomUUID(),
        targetPlayerId: playerId,
        payload: {
          case: 'getPlayerSnapshotRequest',
          value: {},
        },
      }),
    )
  }

  async requestShop(playerId: bigint): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.GET_SHOP, {
      case: 'getShopRequest',
      value: {},
    })
  }

  async buySeeds(
    playerId: bigint,
    shopEntryId: number,
    quantity: number,
    expectedPriceVersion: bigint,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.BUY_SEEDS, {
      case: 'buySeedsRequest',
      value: { shopEntryId, quantity, expectedPriceVersion },
    })
  }

  async buyFertilizer(
    playerId: bigint,
    shopEntryId: number,
    quantity: number,
    expectedPriceVersion: bigint,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.BUY_FERTILIZER, {
      case: 'buyFertilizerRequest',
      value: { shopEntryId, quantity, expectedPriceVersion },
    })
  }

  async plant(playerId: bigint, plotId: number, seedItemId: number): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.PLANT, {
      case: 'plantRequest',
      value: { plotId, seedItemId },
    })
  }

  async applyFertilizer(
    playerId: bigint,
    plotId: number,
    fertilizerItemId: number,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.APPLY_FERTILIZER, {
      case: 'applyFertilizerRequest',
      value: { plotId, fertilizerItemId },
    })
  }

  async harvest(playerId: bigint, plotId: number): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.HARVEST, {
      case: 'harvestRequest',
      value: { plotId },
    })
  }

  async sellAll(
    playerId: bigint,
    cropItemId: number,
    expectedPriceVersion: bigint,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.SELL_CROP, {
      case: 'sellCropRequest',
      value: {
        cropItemId,
        expectedPriceVersion,
        amount: { case: 'sellAll', value: true },
      },
    })
  }

  async sellQuantity(
    playerId: bigint,
    cropItemId: number,
    quantity: number,
    expectedPriceVersion: bigint,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.SELL_CROP, {
      case: 'sellCropRequest',
      value: {
        cropItemId,
        expectedPriceVersion,
        amount: { case: 'quantity', value: quantity },
      },
    })
  }

  async claimChapterReward(playerId: bigint, chapterId: number): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.CLAIM_CHAPTER_REWARD, {
      case: 'claimChapterRewardRequest',
      value: { chapterId },
    })
  }

  async cleanPlot(playerId: bigint, plotId: number): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.CLEAN_PLOT, {
      case: 'cleanPlotRequest',
      value: { plotId },
    })
  }

  disconnect(): void {
    const socket = this.socket
    this.socket = undefined
    this.rejectAll(new Error('WebSocket 已主动断开'))
    if (
      socket &&
      (socket.readyState === WebSocket.OPEN ||
        socket.readyState === WebSocket.CONNECTING)
    ) {
      socket.close(1000, 'client disconnect')
    }
  }

  private sendGameRequest(
    playerId: bigint,
    action: Action,
    payload: WsEnvelope['payload'],
  ): Promise<WsEnvelope> {
    if (playerId === 0n) {
      return Promise.reject(new Error('authenticated player_id 不能为 0'))
    }
    return this.sendRequest(
      create(WsEnvelopeSchema, {
        protocolVersion: PROTOCOL_VERSION,
        messageKind: MessageKind.REQUEST,
        action,
        requestId: crypto.randomUUID(),
        targetPlayerId: playerId,
        payload,
      }),
    )
  }

  private sendRequest(envelope: WsEnvelope): Promise<WsEnvelope> {
    const socket = this.socket
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      return Promise.reject(new Error('WebSocket 尚未连接'))
    }
    if (!envelope.requestId || this.pending.has(envelope.requestId)) {
      return Promise.reject(new Error('request_id 缺失或重复'))
    }

    return new Promise<WsEnvelope>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(envelope.requestId)
        reject(new Error(`请求超时：${envelope.requestId}`))
      }, REQUEST_TIMEOUT_MS)
      this.pending.set(envelope.requestId, {
        action: envelope.action,
        resolve,
        reject,
        timer,
      })
      try {
        socket.send(toBinary(WsEnvelopeSchema, envelope))
      } catch (error) {
        clearTimeout(timer)
        this.pending.delete(envelope.requestId)
        reject(error instanceof Error ? error : new Error(String(error)))
      }
    })
  }

  private handleMessage(event: MessageEvent): void {
    if (!(event.data instanceof ArrayBuffer)) {
      this.failProtocol('Gateway 返回了非 binary frame')
      return
    }
    if (event.data.byteLength > MAX_MESSAGE_BYTES) {
      this.failProtocol('Gateway 消息超过 64 KiB')
      return
    }

    let envelope: WsEnvelope
    try {
      envelope = fromBinary(WsEnvelopeSchema, new Uint8Array(event.data))
    } catch {
      this.failProtocol('Gateway 返回了无效 Protobuf')
      return
    }
    if (envelope.protocolVersion !== PROTOCOL_VERSION) {
      this.failProtocol('Gateway 响应 envelope 无效')
      return
    }
    if (envelope.messageKind === MessageKind.PUSH) {
      if (
        envelope.action !== Action.PLAYER_STATE_CHANGED ||
        envelope.requestId ||
        envelope.targetPlayerId === 0n ||
        !envelope.stateVersion ||
        envelope.serverTimeMs <= 0n ||
        envelope.error ||
        envelope.payload.case !== 'playerStateChangedPush' ||
        !envelope.payload.value.patch
      ) {
        this.failProtocol('Gateway Push envelope 无效')
        return
      }
      this.playerStateChangedHandler?.(envelope)
      return
    }
    if (envelope.messageKind !== MessageKind.RESPONSE || !envelope.requestId) {
      this.failProtocol('Gateway 响应 envelope 无效')
      return
    }

    const pending = this.pending.get(envelope.requestId)
    if (!pending) {
      return
    }
    if (pending.action !== envelope.action) {
      this.failProtocol('Gateway 响应 action 与请求不一致')
      return
    }
    clearTimeout(pending.timer)
    this.pending.delete(envelope.requestId)
    pending.resolve(envelope)
  }

  private failProtocol(message: string): void {
    this.rejectAll(new Error(message))
    this.socket?.close(1002, message.slice(0, 100))
  }

  private rejectAll(error: Error): void {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer)
      pending.reject(error)
    }
    this.pending.clear()
  }
}
