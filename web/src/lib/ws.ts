import { create, fromBinary, toBinary, type MessageInitShape } from '@bufbuild/protobuf'
import {
  Action,
  MessageKind,
  WsEnvelopeSchema,
  type AuthResponse,
  type WsEnvelope,
} from '../gen/classicfarm/v1/ws/ws_pb'
import { randomUuid } from './uuid'

const PROTOCOL_VERSION = 1
const MAX_MESSAGE_BYTES = 64 * 1024
const REQUEST_TIMEOUT_MS = 10_000

type WsPayloadInit = MessageInitShape<typeof WsEnvelopeSchema>['payload']

type PendingRequest = {
  action: Action
  resolve: (envelope: WsEnvelope) => void
  reject: (error: Error) => void
  timer: ReturnType<typeof setTimeout>
}

export type PlayerStateChangedHandler = (envelope: WsEnvelope) => void
export type FarmPresenceChangedHandler = (envelope: WsEnvelope) => void
export type FarmViewChangedHandler = (envelope: WsEnvelope) => void
export type RedDotChangedHandler = (envelope: WsEnvelope) => void
export type ConnectionHandler = (connected: boolean) => void

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

// Mirrors localConfigUrl in http.ts: the development profile only advertises
// loopback URLs, which a browser on another host cannot dial, so the Vite proxy
// forwards the same path to Gate.
function localGatewayUrl(url: URL): string {
  const isLoopback = url.hostname === '127.0.0.1' || url.hostname === 'localhost'
  if (!import.meta.env.DEV || !isLoopback || url.pathname !== '/ws') {
    return url.href
  }
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}${url.pathname}${url.search}`
}

function validateWebSocketUrl(urlText: string): string {
  const url = new URL(urlText)
  if (url.protocol !== 'ws:' && url.protocol !== 'wss:') {
    throw new Error(`Gateway URL 协议无效：${url.protocol}`)
  }
  return localGatewayUrl(url)
}

export class FarmWebSocket {
  private socket?: WebSocket
  private pending = new Map<string, PendingRequest>()
  private playerStateChangedHandler?: PlayerStateChangedHandler
  private farmPresenceChangedHandler?: FarmPresenceChangedHandler
  private farmViewChangedHandler?: FarmViewChangedHandler
  private redDotChangedHandler?: RedDotChangedHandler
  private connectionHandler?: ConnectionHandler

  get connected(): boolean {
    return this.socket?.readyState === WebSocket.OPEN
  }

  // The UI cannot watch `connected` directly: this class is deliberately not
  // reactive, so a socket that dies between renders would leave the shell
  // looking healthy while every command is refused.
  setConnectionHandler(handler?: ConnectionHandler): void {
    this.connectionHandler = handler
  }

  setPlayerStateChangedHandler(handler?: PlayerStateChangedHandler): void {
    this.playerStateChangedHandler = handler
  }

  setFarmPresenceChangedHandler(handler?: FarmPresenceChangedHandler): void {
    this.farmPresenceChangedHandler = handler
  }

  setFarmViewChangedHandler(handler?: FarmViewChangedHandler): void {
    this.farmViewChangedHandler = handler
  }

  setRedDotChangedHandler(handler?: RedDotChangedHandler): void {
    this.redDotChangedHandler = handler
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
        this.connectionHandler?.(false)
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

    this.connectionHandler?.(true)
    onSocketOpen?.()
    const requestId = randomUuid()
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
        requestId: randomUuid(),
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

  async requestPetPanel(playerId: bigint): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.GET_PET_PANEL, {
      case: 'getPetPanelRequest',
      value: {},
    })
  }

  async buyPet(
    playerId: bigint,
    petId: number,
    expectedConfigVersion: bigint,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.BUY_PET, {
      case: 'buyPetRequest',
      value: { petId, expectedConfigVersion },
    })
  }

  async deployPet(playerId: bigint, petId: number): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.DEPLOY_PET, {
      case: 'deployPetRequest',
      value: { petId },
    })
  }

  async buyPetFood(
    playerId: bigint,
    shopEntryId: number,
    quantity: number,
    expectedPriceVersion: bigint,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.BUY_PET_FOOD, {
      case: 'buyPetFoodRequest',
      value: { shopEntryId, quantity, expectedPriceVersion },
    })
  }

  async feedPet(playerId: bigint): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.FEED_PET, {
      case: 'feedPetRequest',
      value: {},
    })
  }

  async openMailbox(
    playerId: bigint,
    pageSize = 20,
    pageToken = '',
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.OPEN_MAILBOX, {
      case: 'openMailboxRequest',
      value: { pageSize, pageToken },
    })
  }

  async markMailRead(playerId: bigint, mailId: string): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.MARK_MAIL_READ, {
      case: 'markMailReadRequest',
      value: { mailId },
    })
  }

  async claimMail(playerId: bigint, mailId: string): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.CLAIM_MAIL, {
      case: 'claimMailRequest',
      value: { mailId },
    })
  }

  async checkMailboxIndicator(playerId: bigint): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.CHECK_MAILBOX_INDICATOR, {
      case: 'checkMailboxIndicatorRequest',
      value: {},
    })
  }

  async sendFriendGift(
    playerId: bigint,
    recipientPlayerId: bigint,
    cropItemId: number,
    quantity: number,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.SEND_FRIEND_GIFT, {
      case: 'sendFriendGiftRequest',
      value: { recipientPlayerId, cropItemId, quantity },
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

  async catchPest(playerId: bigint, plotId: number): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.CATCH_PEST, {
      case: 'catchPestRequest',
      value: { plotId },
    })
  }

  async createFriendCode(playerId: bigint): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.CREATE_FRIEND_CODE, {
      case: 'createFriendCodeRequest',
      value: {},
    })
  }

  async redeemFriendCode(playerId: bigint, code: string): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.REDEEM_FRIEND_CODE, {
      case: 'redeemFriendCodeRequest',
      value: { code },
    })
  }

  async listFriends(playerId: bigint): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.LIST_FRIENDS, {
      case: 'listFriendsRequest',
      value: {},
    })
  }

  async enterFriendFarm(playerId: bigint, ownerPlayerId: bigint): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.ENTER_FRIEND_FARM, {
      case: 'enterFriendFarmRequest',
      value: { ownerPlayerId },
    })
  }

  async farmHeartbeat(
    playerId: bigint,
    ownerPlayerId: bigint,
    visitId: Uint8Array,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.FARM_HEARTBEAT, {
      case: 'farmHeartbeatRequest',
      value: { ownerPlayerId, visitId },
    })
  }

  async exitFriendFarm(
    playerId: bigint,
    ownerPlayerId: bigint,
    visitId: Uint8Array,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.EXIT_FRIEND_FARM, {
      case: 'exitFriendFarmRequest',
      value: { ownerPlayerId, visitId },
    })
  }

  async stealFriendCrop(
    playerId: bigint,
    ownerPlayerId: bigint,
    visitId: Uint8Array,
    plotId: number,
    expectedCropItemId: number,
    farmViewEpoch: Uint8Array,
    farmViewSeq: bigint,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.STEAL_FRIEND_CROP, {
      case: 'stealFriendCropRequest',
      value: {
        ownerPlayerId,
        visitId,
        plotId,
        expectedCropItemId,
        farmViewEpoch,
        farmViewSeq,
      },
    })
  }

  async applyPestToFriend(
    playerId: bigint,
    ownerPlayerId: bigint,
    visitId: Uint8Array,
    plotId: number,
    pestId: number,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.APPLY_PEST_TO_FRIEND, {
      case: 'applyPestToFriendRequest',
      value: { ownerPlayerId, visitId, plotId, pestId },
    })
  }

  async catchPestForFriend(
    playerId: bigint,
    ownerPlayerId: bigint,
    visitId: Uint8Array,
    plotId: number,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.CATCH_PEST_FOR_FRIEND, {
      case: 'catchPestForFriendRequest',
      value: { ownerPlayerId, visitId, plotId },
    })
  }

  async helpCleanFriendPlot(
    playerId: bigint,
    ownerPlayerId: bigint,
    visitId: Uint8Array,
    plotId: number,
  ): Promise<WsEnvelope> {
    return this.sendGameRequest(playerId, Action.HELP_CLEAN_FRIEND_PLOT, {
      case: 'helpCleanFriendPlotRequest',
      value: { ownerPlayerId, visitId, plotId },
    })
  }

  disconnect(): void {
    const socket = this.socket
    this.socket = undefined
    if (socket) {
      this.connectionHandler?.(false)
    }
    this.rejectAll(new Error('WebSocket 已主动断开'))
    if (
      socket &&
      (socket.readyState === WebSocket.OPEN ||
        socket.readyState === WebSocket.CONNECTING)
    ) {
      socket.close(1000, 'client disconnect')
    }
  }

  // Callers pass plain init objects, not constructed messages, so the parameter
  // has to be the init shape rather than WsEnvelope['payload'].
  private sendGameRequest(
    playerId: bigint,
    action: Action,
    payload: WsPayloadInit,
  ): Promise<WsEnvelope> {
    if (playerId === 0n) {
      return Promise.reject(new Error('authenticated player_id 不能为 0'))
    }
    return this.sendRequest(
      create(WsEnvelopeSchema, {
        protocolVersion: PROTOCOL_VERSION,
        messageKind: MessageKind.REQUEST,
        action,
        requestId: randomUuid(),
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
        envelope.requestId ||
        envelope.targetPlayerId === 0n ||
        envelope.serverTimeMs <= 0n ||
        envelope.error
      ) {
        this.failProtocol('Gateway Push envelope 无效')
        return
      }
      if (envelope.action === Action.PLAYER_STATE_CHANGED) {
        if (!envelope.stateVersion || envelope.payload.case !== 'playerStateChangedPush' ||
          !envelope.payload.value.patch) {
          this.failProtocol('Gateway Push envelope 无效')
          return
        }
        this.playerStateChangedHandler?.(envelope)
        return
      }
      if (envelope.action === Action.FARM_PRESENCE_CHANGED) {
        if (
          envelope.stateVersion ||
          envelope.payload.case !== 'farmPresenceChangedPush' ||
          envelope.payload.value.ownerPlayerId !== envelope.targetPlayerId
        ) {
          this.failProtocol('Gateway Push envelope 无效')
          return
        }
        this.farmPresenceChangedHandler?.(envelope)
        return
      }
      if (envelope.action === Action.FARM_VIEW_CHANGED) {
        if (
          envelope.stateVersion ||
          envelope.payload.case !== 'farmViewChangedPush' ||
          envelope.payload.value.ownerPlayerId === 0n ||
          !envelope.payload.value.version ||
          envelope.payload.value.version.farmViewEpoch.byteLength === 0 ||
          envelope.payload.value.version.farmViewSeq === 0n
        ) {
          this.failProtocol('Gateway Push envelope 无效')
          return
        }
        this.farmViewChangedHandler?.(envelope)
        return
      }
      if (envelope.action === Action.RED_DOT_CHANGED) {
        if (
          envelope.stateVersion ||
          envelope.payload.case !== 'redDotChangedPush' ||
          !envelope.payload.value.notificationId
        ) {
          this.failProtocol('Gateway Push envelope 无效')
          return
        }
        this.redDotChangedHandler?.(envelope)
        return
      }
      this.failProtocol('Gateway Push envelope 无效')
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
