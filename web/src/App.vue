<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type {
  ClientConfigPackage,
  GatewayEndpoint,
  SessionView,
} from './gen/classicfarm/v1/http/http_pb'
import type {
  PlayerSnapshot,
  PlayerStatePatch,
  ShopEntryView,
  StateVersion,
  WsEnvelope,
  Error as WsError,
} from './gen/classicfarm/v1/ws/ws_pb'
import { ErrorCode } from './gen/classicfarm/v1/ws/ws_pb'
import {
  authenticate,
  downloadClientConfig,
  fetchBootstrap,
  fetchCsrf,
  issueWsTicket,
  selectGateway,
} from './lib/http'
import { bytesEqual } from './lib/hash'
import { FarmWebSocket } from './lib/ws'
import FarmDashboard, {
  type FarmAction,
  type FarmActionRequest,
} from './components/FarmDashboard.vue'

type Phase =
  | 'idle'
  | 'csrf'
  | 'session'
  | 'bootstrap'
  | 'config'
  | 'ticket'
  | 'socket'
  | 'auth'
  | 'snapshot'
  | 'ready'
  | 'disconnected'
  | 'failed'

const phaseLabels: Record<Phase, string> = {
  idle: '等待账号操作',
  csrf: '获取 CSRF',
  session: '建立 HTTP Session',
  bootstrap: '读取 bootstrap',
  config: '校验客户端配置',
  ticket: '签发一次性 Ticket',
  socket: '连接 Gateway',
  auth: 'WebSocket AUTH',
  snapshot: '请求玩家快照',
  ready: '快照链路完成',
  disconnected: '已断开',
  failed: '链路失败',
}

const steps: Phase[] = [
  'csrf',
  'session',
  'bootstrap',
  'config',
  'ticket',
  'socket',
  'auth',
  'snapshot',
  'ready',
]

const mode = ref<'register' | 'login'>('login')
const accountName = ref('')
const password = ref('')
const phase = ref<Phase>('idle')
const busy = ref(false)
const errorMessage = ref('')
const csrfToken = ref('')
const session = ref<SessionView>()
const gateway = ref<GatewayEndpoint>()
const clientConfig = ref<ClientConfigPackage>()
const authRequestId = ref('')
const snapshotRequestId = ref('')
const snapshot = ref<PlayerSnapshot>()
const stateVersion = ref<StateVersion>()
const serverTimeMs = ref<bigint>()
const wsError = ref<WsError>()
const socket = new FarmWebSocket()
const pushCount = ref(0)
const gapRecoveryCount = ref(0)
const lastPushReason = ref<number>()
const shopEntries = ref<ShopEntryView[]>([])
const busyAction = ref<FarmActionRequest>()
const actionMessage = ref('')
const actionError = ref('')
const lastActionRequestId = ref('')
const nowMs = ref(BigInt(Date.now()))
let serverClockOffsetMs = 0n
let clockTimer: ReturnType<typeof setInterval> | undefined
let gapRecovery: Promise<void> | undefined

socket.setPlayerStateChangedHandler(handlePlayerStateChanged)

const canConnect = computed(() => Boolean(session.value) && !busy.value)
const phaseIndex = computed(() => steps.indexOf(phase.value))

function stepState(step: Phase): 'done' | 'active' | 'waiting' {
  const index = steps.indexOf(step)
  if (phase.value === 'ready' || index < phaseIndex.value) {
    return 'done'
  }
  if (step === phase.value) {
    return 'active'
  }
  return 'waiting'
}

function clearResult(): void {
  errorMessage.value = ''
  authRequestId.value = ''
  snapshotRequestId.value = ''
  snapshot.value = undefined
  stateVersion.value = undefined
  serverTimeMs.value = undefined
  wsError.value = undefined
  pushCount.value = 0
  gapRecoveryCount.value = 0
  lastPushReason.value = undefined
  shopEntries.value = []
  busyAction.value = undefined
  actionMessage.value = ''
  actionError.value = ''
  lastActionRequestId.value = ''
}

function applyPatch(current: PlayerSnapshot, patch: PlayerStatePatch): PlayerSnapshot {
  const removedItems = new Set(patch.inventoryRemovedItemIds)
  const inventory = new Map(
    current.inventory
      .filter((item) => !removedItems.has(item.itemId))
      .map((item) => [item.itemId, item]),
  )
  for (const item of patch.inventoryUpserts) {
    inventory.set(item.itemId, item)
  }
  const plots = new Map(current.plots.map((plot) => [plot.plotId, plot]))
  for (const plot of patch.plotUpserts) {
    plots.set(plot.plotId, plot)
  }
  return {
    ...current,
    coinBalance: patch.coinBalance ?? current.coinBalance,
    inventory: [...inventory.values()].sort((left, right) => left.itemId - right.itemId),
    plots: [...plots.values()].sort((left, right) => left.plotId - right.plotId),
    currentChapter: patch.currentChapter ?? current.currentChapter,
  }
}

function setServerClock(serverMs: bigint): void {
  if (serverMs <= 0n) {
    return
  }
  serverClockOffsetMs = serverMs - BigInt(Date.now())
  nowMs.value = serverMs
}

async function refreshShop(): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected) {
    return
  }
  const response = await socket.requestShop(playerId)
  setServerClock(response.serverTimeMs)
  if (response.error) {
    throw new Error(`商店请求失败：${describeWsError(response.error)}`)
  }
  if (response.payload.case !== 'getShopResponse') {
    throw new Error('商店响应 payload 无效')
  }
  shopEntries.value = response.payload.value.entries
}

function describeWsError(error: WsError): string {
  const labels: Partial<Record<ErrorCode, string>> = {
    [ErrorCode.INVALID_ARGUMENT]: '请求参数无效',
    [ErrorCode.REQUEST_ID_CONFLICT]: '请求编号冲突',
    [ErrorCode.CONFIG_UNAVAILABLE]: '游戏配置暂不可用',
    [ErrorCode.SHOP_ENTRY_NOT_FOUND]: '商品不存在',
    [ErrorCode.SHOP_ENTRY_DISABLED]: '商品已下架',
    [ErrorCode.PRICE_CHANGED]: '价格已经变化，请刷新商店',
    [ErrorCode.INSUFFICIENT_COINS]: '金币不足',
    [ErrorCode.INVENTORY_TYPE_LIMIT]: '仓库种类已满',
    [ErrorCode.INVENTORY_STACK_LIMIT]: '该物品堆叠已满',
    [ErrorCode.ITEM_NOT_OWNED]: '未拥有该物品',
    [ErrorCode.INSUFFICIENT_ITEM_QUANTITY]: '物品数量不足',
    [ErrorCode.PLOT_NOT_FOUND]: '地块不存在',
    [ErrorCode.PLOT_STATE_CONFLICT]: '当前地块状态不能执行此操作',
    [ErrorCode.FERTILIZER_ALREADY_ACTIVE]: '肥料效果仍在生效',
    [ErrorCode.CROP_NOT_MATURE]: '作物尚未成熟',
    [ErrorCode.CHAPTER_NOT_CLAIMABLE]: '章节任务尚未完成',
    [ErrorCode.CHAPTER_REWARD_ALREADY_CLAIMED]: '章节奖励已经领取',
  }
  return labels[error.code] ?? `WebSocket 错误 ${error.code}`
}

function responsePatch(response: WsEnvelope): PlayerStatePatch | undefined {
  switch (response.payload.case) {
    case 'buySeedsResponse':
    case 'plantResponse':
    case 'applyFertilizerResponse':
    case 'harvestResponse':
    case 'sellCropResponse':
    case 'claimChapterRewardResponse':
    case 'cleanPlotResponse':
      return response.payload.value.patch
    default:
      return undefined
  }
}

async function acceptMutationResponse(response: WsEnvelope): Promise<void> {
  setServerClock(response.serverTimeMs)
  lastActionRequestId.value = response.requestId
  wsError.value = response.error
  if (response.error) {
    if (response.error.code === ErrorCode.PRICE_CHANGED) {
      await refreshShop()
    }
    throw new Error(describeWsError(response.error))
  }
  const patch = responsePatch(response)
  const nextVersion = response.stateVersion
  const currentVersion = stateVersion.value
  const currentSnapshot = snapshot.value
  if (!patch || !nextVersion || !currentVersion || !currentSnapshot) {
    throw new Error('写命令响应缺少 patch 或 state_version')
  }
  if (
    nextVersion.ownerEpoch < currentVersion.ownerEpoch ||
    (nextVersion.ownerEpoch === currentVersion.ownerEpoch &&
      nextVersion.playerSeq <= currentVersion.playerSeq)
  ) {
    return
  }
  if (
    nextVersion.ownerEpoch !== currentVersion.ownerEpoch ||
    nextVersion.playerSeq !== currentVersion.playerSeq + 1n
  ) {
    await recoverSnapshotGap()
    return
  }
  snapshot.value = applyPatch(currentSnapshot, patch)
  stateVersion.value = nextVersion
}

function actionSuccessMessage(action: FarmAction, response: WsEnvelope): string {
  switch (action) {
    case 'buy':
      return `已购买 ${response.payload.case === 'buySeedsResponse' ? response.payload.value.quantity : 0} 粒种子，任务进度已更新。`
    case 'plant':
      return '种植成功，作物开始成长。'
    case 'fertilize':
      return '施肥成功，等待服务器成熟 Push。'
    case 'harvest':
      return `收获成功，获得 ${response.payload.case === 'harvestResponse' ? response.payload.value.harvestedQuantity : 0} 个作物。`
    case 'sell':
      return response.payload.case === 'sellCropResponse'
        ? `已出售 ${response.payload.value.soldQuantity} 个作物，获得 ${response.payload.value.totalPrice} 金币。`
        : '出售成功。'
    case 'claim': {
      const pending =
        response.payload.case === 'claimChapterRewardResponse'
          ? response.payload.value.itemsPendingMail.length
          : 0
      return pending > 0
        ? '奖励已领取，仓库溢出物品正在等待邮件处理。'
        : '章节奖励已领取，第二章已激活。'
    }
    case 'clean':
      return '地块清理完成，服务端单玩家闭环已完成。'
  }
}

async function runFarmAction(request: FarmActionRequest): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected || busyAction.value) {
    return
  }
  const { action, plotId } = request
  if (
    (action === 'plant' || action === 'fertilize' || action === 'harvest' || action === 'clean') &&
    !plotId
  ) {
    actionError.value = '地块编号缺失'
    return
  }
  busyAction.value = request
  actionMessage.value = ''
  actionError.value = ''
  try {
    let response: WsEnvelope
    switch (action) {
      case 'buy': {
        const quote = shopEntries.value.find((entry) => entry.itemId === 1001)
        if (!quote) throw new Error('种子报价尚未加载')
        response = await socket.buySeeds(
          playerId,
          quote.shopEntryId,
          request.quantity ?? 1,
          quote.priceVersion,
        )
        break
      }
      case 'plant':
        response = await socket.plant(playerId, plotId!, 1001)
        break
      case 'fertilize':
        response = await socket.applyFertilizer(playerId, plotId!, 1)
        break
      case 'harvest':
        response = await socket.harvest(playerId, plotId!)
        break
      case 'sell': {
        const quote = shopEntries.value.find((entry) => entry.itemId === 1002)
        if (!quote) throw new Error('作物收购报价尚未加载')
        response = request.sellAll
          ? await socket.sellAll(playerId, 1002, quote.priceVersion)
          : await socket.sellQuantity(
              playerId,
              1002,
              request.quantity ?? 1,
              quote.priceVersion,
            )
        break
      }
      case 'claim':
        response = await socket.claimChapterReward(
          playerId,
          snapshot.value?.currentChapter?.chapterId ?? 0,
        )
        break
      case 'clean':
        response = await socket.cleanPlot(playerId, plotId!)
        break
    }
    await acceptMutationResponse(response)
    actionMessage.value = actionSuccessMessage(action, response)
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : String(error)
  } finally {
    busyAction.value = undefined
  }
}

function handlePlayerStateChanged(envelope: WsEnvelope): void {
  const version = envelope.stateVersion
  const currentVersion = stateVersion.value
  const currentSnapshot = snapshot.value
  if (
    !version ||
    envelope.targetPlayerId !== session.value?.playerId ||
    envelope.payload.case !== 'playerStateChangedPush'
  ) {
    return
  }
  if (!currentVersion || !currentSnapshot) {
    void recoverSnapshotGap()
    return
  }
  if (
    version.ownerEpoch < currentVersion.ownerEpoch ||
    (version.ownerEpoch === currentVersion.ownerEpoch &&
      version.playerSeq <= currentVersion.playerSeq)
  ) {
    return
  }
  if (
    version.ownerEpoch !== currentVersion.ownerEpoch ||
    version.playerSeq !== currentVersion.playerSeq + 1n
  ) {
    void recoverSnapshotGap()
    return
  }
  snapshot.value = applyPatch(currentSnapshot, envelope.payload.value.patch!)
  stateVersion.value = version
  serverTimeMs.value = envelope.serverTimeMs
  setServerClock(envelope.serverTimeMs)
  lastPushReason.value = envelope.payload.value.reason
  pushCount.value += 1
  actionMessage.value = '作物已经成熟，可以收获。'
}

function recoverSnapshotGap(): Promise<void> {
  if (gapRecovery) {
    return gapRecovery
  }
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected) {
    return Promise.resolve()
  }
  gapRecoveryCount.value += 1
  gapRecovery = socket
    .requestPlayerSnapshot(playerId)
    .then((response) => {
      if (
        response.error ||
        !response.stateVersion ||
        response.payload.case !== 'getPlayerSnapshotResponse' ||
        !response.payload.value.snapshot
      ) {
        throw new Error('Push 版本缺口恢复快照失败')
      }
      snapshot.value = response.payload.value.snapshot
      stateVersion.value = response.stateVersion
      serverTimeMs.value = response.serverTimeMs
      setServerClock(response.serverTimeMs)
    })
    .catch((error) => {
      phase.value = 'failed'
      errorMessage.value = error instanceof Error ? error.message : String(error)
    })
    .finally(() => {
      gapRecovery = undefined
    })
  return gapRecovery
}

async function establishSnapshot(): Promise<void> {
  if (!session.value) {
    throw new Error('请先注册或登录')
  }
  clearResult()
  socket.disconnect()

  phase.value = 'csrf'
  csrfToken.value = (await fetchCsrf()).csrfToken

  phase.value = 'bootstrap'
  const bootstrap = await fetchBootstrap()
  const expectedAuth = bootstrap.authBootstrap
  if (!expectedAuth || expectedAuth.playerId !== session.value.playerId) {
    throw new Error('bootstrap player_id 与 Session 不一致')
  }
  if (expectedAuth.protocolMin > 1 || expectedAuth.protocolMax < 1) {
    throw new Error('bootstrap 不支持 WebSocket 协议 V1')
  }

  phase.value = 'config'
  clientConfig.value = await downloadClientConfig(
    expectedAuth.clientConfigUrl,
    expectedAuth.clientConfigSha256,
    expectedAuth.clientConfigVersion,
  )

  gateway.value = selectGateway(bootstrap.gateways)
  phase.value = 'ticket'
  const ticket = await issueWsTicket(gateway.value.gatewayId, csrfToken.value)

  phase.value = 'socket'
  const connection = await socket.connectAndAuth(
    gateway.value.websocketUrl,
    ticket.wsTicket,
    session.value.playerId,
    () => {
      phase.value = 'auth'
    },
  )
  authRequestId.value = connection.requestId

  const authConfigChanged =
    connection.auth.clientConfigVersion !== expectedAuth.clientConfigVersion ||
    connection.auth.clientConfigUrl !== expectedAuth.clientConfigUrl ||
    !bytesEqual(connection.auth.clientConfigSha256, expectedAuth.clientConfigSha256)
  if (authConfigChanged) {
    phase.value = 'config'
    clientConfig.value = await downloadClientConfig(
      connection.auth.clientConfigUrl,
      connection.auth.clientConfigSha256,
      connection.auth.clientConfigVersion,
    )
  }

  phase.value = 'snapshot'
  const response = await socket.requestPlayerSnapshot(connection.auth.playerId)
  snapshotRequestId.value = response.requestId
  serverTimeMs.value = response.serverTimeMs
  setServerClock(response.serverTimeMs)
  stateVersion.value = response.stateVersion
  wsError.value = response.error
  if (response.error) {
    throw new Error(`快照请求失败：WebSocket 错误 ${response.error.code}`)
  }
  if (
    response.payload.case !== 'getPlayerSnapshotResponse' ||
    !response.payload.value.snapshot ||
    !response.stateVersion
  ) {
    throw new Error('快照响应缺少 payload 或 state_version')
  }
  if (response.payload.value.snapshot.playerId !== connection.auth.playerId) {
    throw new Error('快照 player_id 与认证身份不一致')
  }
  snapshot.value = response.payload.value.snapshot
  await refreshShop()
  phase.value = 'ready'
}

async function submitCredentials(): Promise<void> {
  busy.value = true
  clearResult()
  try {
    phase.value = 'csrf'
    csrfToken.value = (await fetchCsrf()).csrfToken
    phase.value = 'session'
    session.value = await authenticate(
      mode.value,
      accountName.value,
      password.value,
      csrfToken.value,
    )
    password.value = ''

    // Successful registration/login rotates the token; never reuse the pre-auth value.
    csrfToken.value = (await fetchCsrf()).csrfToken
    await establishSnapshot()
  } catch (error) {
    phase.value = 'failed'
    errorMessage.value = error instanceof Error ? error.message : String(error)
  } finally {
    busy.value = false
  }
}

async function reconnect(): Promise<void> {
  busy.value = true
  try {
    await establishSnapshot()
  } catch (error) {
    phase.value = 'failed'
    errorMessage.value = error instanceof Error ? error.message : String(error)
  } finally {
    busy.value = false
  }
}

function disconnect(): void {
  socket.disconnect()
  phase.value = 'disconnected'
}

onMounted(() => {
  clockTimer = setInterval(() => {
    nowMs.value = BigInt(Date.now()) + serverClockOffsetMs
  }, 1000)
})

onBeforeUnmount(() => {
  if (clockTimer) {
    clearInterval(clockTimer)
  }
  socket.disconnect()
})
</script>

<template>
  <main class="shell">
    <header class="hero">
      <div>
        <p class="eyebrow">V3 · PLAYER ACTOR FARM</p>
        <h1>Classic Farm</h1>
        <p class="summary">
          注册或登录后，在同一个 Player Actor 中完成购买、种植、成长、收获、出售、领奖与清理。
        </p>
      </div>
      <span class="phase-badge" :data-phase="phase">{{ phaseLabels[phase] }}</span>
    </header>

    <FarmDashboard
      v-if="snapshot"
      :snapshot="snapshot"
      :shop-entries="shopEntries"
      :connected="socket.connected"
      :busy-action="busyAction"
      :action-message="actionMessage"
      :action-error="actionError"
      :now-ms="nowMs"
      @action="runFarmAction"
    />

    <section class="card auth-card" aria-labelledby="auth-title">
      <div class="section-heading">
        <div>
          <p class="eyebrow">01 · HTTP SESSION</p>
          <h2 id="auth-title">账号认证</h2>
        </div>
        <div class="mode-switch" aria-label="认证方式">
          <button
            type="button"
            :class="{ selected: mode === 'login' }"
            @click="mode = 'login'"
          >
            登录
          </button>
          <button
            type="button"
            :class="{ selected: mode === 'register' }"
            @click="mode = 'register'"
          >
            注册
          </button>
        </div>
      </div>

      <form @submit.prevent="submitCredentials">
        <label>
          账号
          <input
            v-model="accountName"
            autocomplete="username"
            minlength="3"
            maxlength="32"
            pattern="[a-z][a-z0-9_]{2,31}"
            placeholder="lowercase_account"
            required
          />
        </label>
        <label>
          密码
          <input
            v-model="password"
            :autocomplete="mode === 'register' ? 'new-password' : 'current-password'"
            type="password"
            minlength="12"
            maxlength="128"
            placeholder="至少 12 个字符"
            required
          />
        </label>
        <button class="primary" type="submit" :disabled="busy">
          {{ busy ? '处理中…' : mode === 'register' ? '注册并连接' : '登录并连接' }}
        </button>
      </form>

      <dl v-if="session" class="facts compact">
        <div><dt>player_id</dt><dd>{{ session.playerId.toString() }}</dd></div>
        <div><dt>account</dt><dd>{{ session.accountName }}</dd></div>
      </dl>
    </section>

    <section class="card" aria-labelledby="progress-title">
      <div class="section-heading">
        <div>
          <p class="eyebrow">02 · CONNECTION</p>
          <h2 id="progress-title">连接阶段</h2>
        </div>
        <div class="connection-actions">
          <button type="button" :disabled="!socket.connected" @click="disconnect">
            断开
          </button>
          <button type="button" :disabled="!canConnect" @click="reconnect">
            重新取 Ticket
          </button>
        </div>
      </div>

      <ol class="timeline">
        <li v-for="step in steps" :key="step" :data-state="stepState(step)">
          <span class="dot" aria-hidden="true"></span>
          <span>{{ phaseLabels[step] }}</span>
        </li>
      </ol>

      <p v-if="errorMessage" class="error-banner" role="alert">{{ errorMessage }}</p>
    </section>

    <section class="card snapshot-card" aria-labelledby="snapshot-title">
      <div class="section-heading">
        <div>
          <p class="eyebrow">03 · ACTOR DIAGNOSTICS</p>
          <h2 id="snapshot-title">玩家快照</h2>
        </div>
        <span class="proof-label">live state</span>
      </div>

      <dl class="facts">
        <div><dt>Gateway</dt><dd>{{ gateway?.gatewayId ?? '—' }}</dd></div>
        <div><dt>AUTH request_id</dt><dd>{{ authRequestId || '—' }}</dd></div>
        <div><dt>Snapshot request_id</dt><dd>{{ snapshotRequestId || '—' }}</dd></div>
        <div><dt>Last action request_id</dt><dd>{{ lastActionRequestId || '—' }}</dd></div>
        <div>
          <dt>state_version</dt>
          <dd>
            {{
              stateVersion
                ? `${stateVersion.ownerEpoch.toString()} / ${stateVersion.playerSeq.toString()}`
                : '—'
            }}
          </dd>
        </div>
        <div><dt>server_time_ms</dt><dd>{{ serverTimeMs?.toString() ?? '—' }}</dd></div>
        <div><dt>error</dt><dd>{{ wsError ? wsError.code : '—' }}</dd></div>
        <div><dt>config_version</dt><dd>{{ clientConfig?.clientConfigVersion.toString() ?? '—' }}</dd></div>
        <div><dt>Push 数量</dt><dd>{{ pushCount }}</dd></div>
        <div><dt>最后 Push 原因</dt><dd>{{ lastPushReason ?? '—' }}</dd></div>
        <div><dt>缺口快照恢复</dt><dd>{{ gapRecoveryCount }}</dd></div>
      </dl>

      <div v-if="snapshot" class="snapshot-grid">
        <article>
          <span>玩家</span>
          <strong>{{ snapshot.playerId.toString() }}</strong>
        </article>
        <article>
          <span>金币</span>
          <strong>{{ snapshot.coinBalance.toString() }}</strong>
        </article>
        <article>
          <span>库存项</span>
          <strong>{{ snapshot.inventory.length }}</strong>
        </article>
        <article>
          <span>地块</span>
          <strong>{{ snapshot.plots.length }}</strong>
        </article>
      </div>
      <p v-else class="empty-state">完成认证后，这里展示 Actor 返回的最小玩家投影。</p>
    </section>
  </main>
</template>
