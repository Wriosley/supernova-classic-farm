<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type {
  ClientConfigPackage,
  GatewayEndpoint,
  SessionView,
} from './gen/classicfarm/v1/http/http_pb'
import type {
  CreateFriendCodeResponse,
  FarmViewPatch,
  FarmVisitSnapshot,
  FriendView,
  PlayerSnapshot,
  PlayerStatePatch,
  ShopEntryView,
  StateVersion,
  WsEnvelope,
  Error as WsError,
} from './gen/classicfarm/v1/ws/ws_pb'
import {
  EffectViewSchema,
  ErrorCode,
  FarmPresenceKind,
  RedDotCategory,
  RedDotOperation,
} from './gen/classicfarm/v1/ws/ws_pb'
import { create } from '@bufbuild/protobuf'
import { HttpErrorCode } from './gen/classicfarm/v1/http/http_pb'
import {
  ProtobufHttpError,
  authenticate,
  downloadClientConfig,
  fetchBootstrap,
  fetchCsrf,
  fetchSession,
  issueWsTicket,
  logout,
  selectGateway,
} from './lib/http'
import { bytesEqual } from './lib/hash'
import { decideFarmViewPatch } from './lib/farm-view'
import { mutationResponsePatch } from './lib/mutation-response'
import { FarmWebSocket } from './lib/ws'
import type { FarmAction, FarmActionRequest } from './lib/farm-actions'
import { panelKickers, panelTitles, type PanelId } from './lib/panels'
import { usePlotFloats } from './lib/plot-floats'
import {
  captureInviteFriendCodeFromLocation,
  clearPendingFriendCode,
  loadPendingFriendCode,
} from './lib/friend-invite'
import AccountPanel from './components/AccountPanel.vue'
import CompendiumPanel from './components/CompendiumPanel.vue'
import FarmDashboard from './components/FarmDashboard.vue'
import type { DeployedPet } from './lib/pet-art'
import FriendFarmDashboard from './components/FriendFarmDashboard.vue'
import FriendGiftPanel from './components/FriendGiftPanel.vue'
import FriendsPanel from './components/FriendsPanel.vue'
import GameDrawer from './components/GameDrawer.vue'
import InventoryPanel from './components/InventoryPanel.vue'
import MailboxPanel from './components/MailboxPanel.vue'
import PetPanel from './components/PetPanel.vue'
import PlayerProfileModal from './components/PlayerProfileModal.vue'
import ShopPanel from './components/ShopPanel.vue'
import TaskPanel from './components/TaskPanel.vue'
import TopNav from './components/TopNav.vue'
import type {
  CropCatalogEntryView,
  MailView,
  PetPanelView,
  PetShopEntryView,
} from './gen/classicfarm/v1/ws/ws_pb'

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
const connected = ref(false)
const pushCount = ref(0)
const gapRecoveryCount = ref(0)
const lastPushReason = ref<number>()
const shopEntries = ref<ShopEntryView[]>([])
const cropCatalog = ref<CropCatalogEntryView[]>([])
const profileOpen = ref(false)
const busyAction = ref<FarmActionRequest>()
const actionMessage = ref('')
const actionError = ref('')
const lastActionRequestId = ref('')
const nowMs = ref(BigInt(Date.now()))
let serverClockOffsetMs = 0n
let clockTimer: ReturnType<typeof setInterval> | undefined
let gapRecovery: Promise<void> | undefined

const HEARTBEAT_INTERVAL_MS = 30_000
// sessionStorage survives a tab reload (unlike the in-memory visit_* refs) but
// is scoped to this tab, so a second account in another tab cannot steal the
// restored visit target. visit_id itself is deliberately not stored: Zone's
// VisitorRegistry is in-memory with a 90s TTL, so a reload always needs a
// fresh ENTER_FRIEND_FARM; remembering only the owner is enough.
const PENDING_VISIT_STORAGE_KEY = 'classic-farm:pending-visit-owner'
// sessionStorage survives F5 in the same tab, but is wiped when the tab is
// closed. That is the signal we use to distinguish "reload keep login" from
// "re-open browser / new tab must start logged out". The HttpOnly Session
// cookie alone cannot express this: it outlives tab close.
const TAB_SESSION_STORAGE_KEY = 'classic-farm:tab-session'

const friends = ref<FriendView[]>([])
const friendsBusy = ref(false)
const friendsError = ref('')
const generatedFriendCode = ref<CreateFriendCodeResponse>()
const inviteNotice = ref('')
const autoRedeemBusy = ref(false)
const redeemBusy = ref(false)
const redeemMessage = ref('')
const redeemError = ref('')
const visitOwnerId = ref<bigint>()
const visitId = ref<Uint8Array>()
const visitSnapshot = ref<FarmVisitSnapshot>()
const visitBusy = ref(false)
const visitError = ref('')
const stealBusyPlotId = ref<number>()
const stealError = ref('')
const stealMessage = ref('')
const { floats: farmFloats, pushFloat: pushFarmFloat } = usePlotFloats()
const {
  floats: visitFloats,
  pushFloat: pushVisitFloat,
  clearFloats: clearVisitFloats,
} = usePlotFloats()
const farmVisitorEntries = ref<
  Map<string, { playerId?: bigint; accountName?: string }>
>(new Map())
const petPanel = ref<PetPanelView | null>(null)
const petBusyBuyId = ref<number | null>(null)
const petBusyDeployId = ref<number | null>(null)
const petBusyBuyFood = ref(false)
const petBusyFeed = ref(false)
const petError = ref('')
const petMessage = ref('')
const petFloatText = ref('')
let petFloatTimer: ReturnType<typeof setTimeout> | undefined
const mailRedDot = ref(false)
const friendFarmRedDots = ref<Set<string>>(new Set())
const activePanel = ref<PanelId | null>(null)
const mailboxMails = ref<MailView[]>([])
const mailboxNextPageToken = ref('')
const mailboxFilter = ref<'all' | 'public' | 'private' | 'gift'>('all')
const mailboxLoading = ref(false)
const mailboxLoadingMore = ref(false)
const mailboxClaimingId = ref<string | null>(null)
const mailboxError = ref('')
const mailboxMessage = ref('')
const giftOpen = ref(false)
const giftRecipientId = ref<bigint>(0n)
const giftRecipientName = ref('')
const giftBusy = ref(false)
const giftError = ref('')
const giftMessage = ref('')
let heartbeatTimer: ReturnType<typeof setInterval> | undefined

socket.setConnectionHandler((value) => {
  connected.value = value
  if (!value) {
    farmVisitorEntries.value = new Map()
  }
})
socket.setPlayerStateChangedHandler(handlePlayerStateChanged)
socket.setFarmPresenceChangedHandler(handleFarmPresenceChanged)
socket.setFarmViewChangedHandler(handleFarmViewChanged)
socket.setRedDotChangedHandler(handleRedDotChanged)

const canConnect = computed(() => Boolean(session.value) && !busy.value)
const phaseIndex = computed(() => steps.indexOf(phase.value))
const visiting = computed(() => visitOwnerId.value !== undefined)
// The game shell owns the screen as soon as an authoritative snapshot exists;
// a dropped socket keeps the farm on screen and offers 重新连接 in 账号 instead
// of throwing the player back to the login form.
const signedIn = computed(() => Boolean(session.value && snapshot.value))
const timelineSteps = computed(() =>
  steps.map((step) => ({ label: phaseLabels[step], state: stepState(step) })),
)
const diagnosticFacts = computed(() => [
  { label: 'Gateway', value: gateway.value?.gatewayId ?? '—' },
  { label: 'AUTH request_id', value: authRequestId.value || '—' },
  { label: 'Snapshot request_id', value: snapshotRequestId.value || '—' },
  { label: 'Last action request_id', value: lastActionRequestId.value || '—' },
  {
    label: 'state_version',
    value: stateVersion.value
      ? `${stateVersion.value.ownerEpoch.toString()} / ${stateVersion.value.playerSeq.toString()}`
      : '—',
  },
  { label: 'server_time_ms', value: serverTimeMs.value?.toString() ?? '—' },
  { label: 'error', value: wsError.value ? String(wsError.value.code) : '—' },
  {
    label: 'config_version',
    value: clientConfig.value?.clientConfigVersion.toString() ?? '—',
  },
  { label: 'Push 数量', value: String(pushCount.value) },
  { label: '最后 Push 原因', value: lastPushReason.value?.toString() ?? '—' },
  { label: '缺口快照恢复', value: String(gapRecoveryCount.value) },
  { label: '库存项', value: String(snapshot.value?.inventory.length ?? 0) },
  { label: '地块', value: String(snapshot.value?.plots.length ?? 0) },
])
const friendRedDot = computed(() => friendFarmRedDots.value.size > 0)
// The farm stays on screen when the socket dies, so the shell must say so
// itself; otherwise every panel just looks empty and every button dead.
const shellNotice = computed(() => {
  if (connected.value) {
    return errorMessage.value
  }
  if (phase.value === 'failed') {
    return errorMessage.value || '连接失败'
  }
  if (phase.value === 'ready' || phase.value === 'disconnected' || phase.value === 'idle') {
    return '实时连接已断开，命令暂时无法发送。'
  }
  return `${phaseLabels[phase.value]}…`
})

function togglePanel(panel: PanelId): void {
  if (activePanel.value === panel) {
    activePanel.value = null
    return
  }
  activePanel.value = panel
  switch (panel) {
    case 'mailbox':
      void openMailbox()
      break
    case 'friends':
      void loadFriends()
      break
    case 'pet':
      if (!petPanel.value) {
        void refreshPetPanel()
      }
      break
    case 'shop':
      void reloadCatalog()
      break
    case 'compendium':
      if (cropCatalog.value.length === 0) {
        void reloadCatalog()
      }
      break
  }
}
const inventoryMap = computed(() => {
  const map = new Map<number, number>()
  for (const item of snapshot.value?.inventory ?? []) {
    map.set(item.itemId, item.quantity)
  }
  return map
})
// The deployed dog lives next to the farm, so the pet panel data has to be
// loaded even when its drawer was never opened (see the post-snapshot loads).
const activePet = computed<DeployedPet | undefined>(() => {
  const panel = petPanel.value
  const petId = panel?.activePetId ?? 0
  if (!panel || petId === 0) {
    return undefined
  }
  return {
    petId,
    name: panel.pets.find((pet) => pet.petId === petId)?.name ?? `宠物#${petId}`,
    foodActiveUntilMs: panel.foodActiveUntilMs,
  }
})
const visitOwnerLabel = computed(() => {
  const ownerId = visitOwnerId.value
  if (ownerId === undefined) {
    return '好友'
  }
  const friend = friends.value.find((entry) => entry.playerId === ownerId)
  return friend?.accountName || `player ${ownerId.toString()}`
})
function farmVisitorLabel(playerId?: bigint, accountName?: string): string {
  const friend = playerId
    ? friends.value.find((entry) => entry.playerId === playerId)
    : undefined
  return (
    accountName ||
    friend?.accountName ||
    (playerId ? `玩家 ${playerId.toString()}` : '一位好友')
  )
}

const farmVisitors = computed(() =>
  [...farmVisitorEntries.value.values()]
    .map((entry) => farmVisitorLabel(entry.playerId, entry.accountName))
    .sort(),
)

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
  cropCatalog.value = []
  profileOpen.value = false
  petPanel.value = null
  petError.value = ''
  petMessage.value = ''
  mailRedDot.value = false
  friendFarmRedDots.value = new Set()
  activePanel.value = null
  mailboxMails.value = []
  mailboxNextPageToken.value = ''
  mailboxFilter.value = 'all'
  mailboxLoading.value = false
  mailboxLoadingMore.value = false
  mailboxClaimingId.value = null
  mailboxError.value = ''
  mailboxMessage.value = ''
  giftOpen.value = false
  giftRecipientId.value = 0n
  giftRecipientName.value = ''
  giftBusy.value = false
  giftError.value = ''
  giftMessage.value = ''
  busyAction.value = undefined
  actionMessage.value = ''
  actionError.value = ''
  lastActionRequestId.value = ''
  friends.value = []
  friendsError.value = ''
  generatedFriendCode.value = undefined
  redeemMessage.value = ''
  redeemError.value = ''
  stopVisitHeartbeat()
  visitOwnerId.value = undefined
  visitId.value = undefined
  visitSnapshot.value = undefined
  visitError.value = ''
  farmVisitorEntries.value = new Map()
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
    career: patch.career ?? current.career,
    cropCompendium: patch.cropCompendium ?? current.cropCompendium,
  }
}

function applyFarmViewPatch(current: FarmVisitSnapshot, patch: FarmViewPatch): FarmVisitSnapshot {
  const plots = new Map(current.plots.map((plot) => [plot.plotId, plot]))
  for (const plot of patch.plotUpserts) {
    plots.set(plot.plotId, plot)
  }
  return {
    ...current,
    version: patch.version,
    plots: [...plots.values()].sort((left, right) => left.plotId - right.plotId),
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
  if (!playerId) {
    return
  }
  if (!socket.connected) {
    throw new Error('尚未连接 Gateway')
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
  cropCatalog.value = response.payload.value.crops
}

async function reloadCatalog(): Promise<void> {
  try {
    await refreshShop()
    if (errorMessage.value.startsWith('商店目录')) {
      errorMessage.value = ''
    }
  } catch (error) {
    errorMessage.value = `商店目录加载失败：${
      error instanceof Error ? error.message : String(error)
    }`
  }
}

async function refreshPetPanel(): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected) {
    return
  }
  const response = await socket.requestPetPanel(playerId)
  setServerClock(response.serverTimeMs)
  if (response.error) {
    petError.value = describeWsError(response.error)
    return
  }
  if (response.payload.case !== 'getPetPanelResponse') {
    petError.value = '宠物面板响应无效'
    return
  }
  petPanel.value = response.payload.value.panel ?? null
  petError.value = ''
}

function applyPetPanelFromResponse(response: WsEnvelope): void {
  switch (response.payload.case) {
    case 'buyPetResponse':
    case 'deployPetResponse':
    case 'buyPetFoodResponse':
    case 'feedPetResponse':
      if (response.payload.value.panel) {
        petPanel.value = response.payload.value.panel
      }
      break
  }
}

async function buyPet(pet: PetShopEntryView): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected || petBusyBuyId.value !== null) {
    return
  }
  petBusyBuyId.value = pet.petId
  petError.value = ''
  petMessage.value = ''
  try {
    const response = await socket.buyPet(playerId, pet.petId, pet.configVersion)
    setServerClock(response.serverTimeMs)
    if (response.error) {
      throw new Error(describeWsError(response.error))
    }
    if (response.payload.case !== 'buyPetResponse') {
      throw new Error('购买宠物响应无效')
    }
    await acceptMutationResponse(response)
    applyPetPanelFromResponse(response)
    petMessage.value = `已购买${pet.name}`
  } catch (error) {
    petError.value = error instanceof Error ? error.message : String(error)
  } finally {
    petBusyBuyId.value = null
  }
}

async function deployPet(petId: number): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected || petBusyDeployId.value !== null) {
    return
  }
  petBusyDeployId.value = petId
  petError.value = ''
  petMessage.value = ''
  try {
    const response = await socket.deployPet(playerId, petId)
    setServerClock(response.serverTimeMs)
    if (response.error) {
      throw new Error(describeWsError(response.error))
    }
    if (response.payload.case !== 'deployPetResponse') {
      throw new Error('派出宠物响应无效')
    }
    await acceptMutationResponse(response)
    applyPetPanelFromResponse(response)
    petMessage.value = '已更新出战宠物'
  } catch (error) {
    petError.value = error instanceof Error ? error.message : String(error)
  } finally {
    petBusyDeployId.value = null
  }
}

async function buyPetFood(): Promise<void> {
  const playerId = session.value?.playerId
  const food = petPanel.value?.petFood
  if (!playerId || !socket.connected || !food || petBusyBuyFood.value) {
    return
  }
  petBusyBuyFood.value = true
  petError.value = ''
  petMessage.value = ''
  try {
    const response = await socket.buyPetFood(
      playerId,
      food.shopEntryId,
      1,
      food.priceVersion,
    )
    setServerClock(response.serverTimeMs)
    if (response.error) {
      throw new Error(describeWsError(response.error))
    }
    if (response.payload.case !== 'buyPetFoodResponse') {
      throw new Error('购买狗粮响应无效')
    }
    await acceptMutationResponse(response)
    applyPetPanelFromResponse(response)
    petMessage.value = '已购买 1 份狗粮'
  } catch (error) {
    petError.value = error instanceof Error ? error.message : String(error)
  } finally {
    petBusyBuyFood.value = false
  }
}

async function feedPet(): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected || petBusyFeed.value) {
    return
  }
  petBusyFeed.value = true
  petError.value = ''
  petMessage.value = ''
  try {
    const response = await socket.feedPet(playerId)
    setServerClock(response.serverTimeMs)
    if (response.error) {
      throw new Error(describeWsError(response.error))
    }
    if (response.payload.case !== 'feedPetResponse') {
      throw new Error('喂食响应无效')
    }
    await acceptMutationResponse(response)
    applyPetPanelFromResponse(response)
    petMessage.value = '喂食成功，狗粮有效期已延长'
  } catch (error) {
    petError.value = error instanceof Error ? error.message : String(error)
  } finally {
    petBusyFeed.value = false
  }
}

function mailItemName(itemId: number): string {
  const crop = cropCatalog.value.find((entry) => entry.cropItemId === itemId)
  if (crop?.name) {
    return crop.name
  }
  return `物品#${itemId}`
}

function clearFriendFarmRedDot(ownerId: bigint): void {
  const key = ownerId.toString()
  if (!friendFarmRedDots.value.has(key)) {
    return
  }
  const next = new Set(friendFarmRedDots.value)
  next.delete(key)
  friendFarmRedDots.value = next
}

function friendHasFarmRedDot(ownerId: bigint): boolean {
  return friendFarmRedDots.value.has(ownerId.toString())
}

// refreshMailRedDot seeds the mailbox indicator at login. RED_DOT_CHANGED is
// only pushed to players who were connected when the mail arrived, and public
// mail is never pushed, so without this query the dot stays dark forever for
// anything that landed while the player was away. A failure here must not
// block a working session: the dot simply keeps its current state.
async function refreshMailRedDot(playerId: bigint): Promise<void> {
  try {
    const response = await socket.checkMailboxIndicator(playerId)
    setServerClock(response.serverTimeMs)
    if (response.error || response.payload.case !== 'checkMailboxIndicatorResponse') {
      return
    }
    if (response.payload.value.hasNewMail) {
      mailRedDot.value = true
    }
  } catch (error) {
    console.warn('邮箱红点查询失败', error)
  }
}

async function openMailbox(): Promise<void> {
  mailRedDot.value = false
  activePanel.value = 'mailbox'
  mailboxFilter.value = 'all'
  mailboxError.value = ''
  mailboxMessage.value = ''
  await refreshMailbox()
}

async function refreshMailbox(pageToken = ''): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected) {
    mailboxError.value = '尚未连接，无法打开邮箱'
    return
  }
  const loadingMore = pageToken !== ''
  if (loadingMore) {
    mailboxLoadingMore.value = true
  } else {
    mailboxLoading.value = true
    mailboxMails.value = []
    mailboxNextPageToken.value = ''
  }
  mailboxError.value = ''
  try {
    const response = await socket.openMailbox(playerId, 20, pageToken)
    setServerClock(response.serverTimeMs)
    if (response.error) {
      throw new Error(describeWsError(response.error))
    }
    if (response.payload.case !== 'openMailboxResponse') {
      throw new Error('打开邮箱响应无效')
    }
    const page = response.payload.value
    mailboxMails.value = loadingMore
      ? [...mailboxMails.value, ...page.mails]
      : page.mails
    mailboxNextPageToken.value = page.nextPageToken
  } catch (error) {
    mailboxError.value = error instanceof Error ? error.message : String(error)
    if (!loadingMore) {
      mailboxMails.value = []
    }
  } finally {
    mailboxLoading.value = false
    mailboxLoadingMore.value = false
  }
}

async function markMailRead(mail: MailView): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected || mail.read) {
    return
  }
  try {
    const response = await socket.markMailRead(playerId, mail.mailId)
    setServerClock(response.serverTimeMs)
    if (response.error) {
      throw new Error(describeWsError(response.error))
    }
    mailboxMails.value = mailboxMails.value.map((entry) =>
      entry.mailId === mail.mailId ? { ...entry, read: true } : entry,
    )
  } catch (error) {
    mailboxError.value = error instanceof Error ? error.message : String(error)
  }
}

async function claimMail(mail: MailView): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected || mailboxClaimingId.value !== null) {
    return
  }
  mailboxClaimingId.value = mail.mailId
  mailboxError.value = ''
  mailboxMessage.value = ''
  try {
    const response = await socket.claimMail(playerId, mail.mailId)
    setServerClock(response.serverTimeMs)
    if (response.error) {
      throw new Error(describeWsError(response.error))
    }
    if (response.payload.case !== 'claimMailResponse') {
      throw new Error('领取邮件响应无效')
    }
    // MailSvr omits state_version when an earlier attempt already applied the
    // reward: the patch cannot be sequenced onto the local snapshot, so reload
    // an authoritative one rather than rejecting a claim that did succeed.
    if (response.stateVersion) {
      await acceptMutationResponse(response)
    } else {
      setServerClock(response.serverTimeMs)
      await recoverSnapshotGap()
    }
    mailboxMails.value = mailboxMails.value.map((entry) =>
      entry.mailId === mail.mailId
        ? { ...entry, read: true, claimed: true }
        : entry,
    )
    mailboxMessage.value = '领取成功'
  } catch (error) {
    mailboxError.value = error instanceof Error ? error.message : String(error)
  } finally {
    mailboxClaimingId.value = null
  }
}

function openGiftPanel(friend: FriendView): void {
  giftRecipientId.value = friend.playerId
  giftRecipientName.value = friend.accountName
  giftError.value = ''
  giftMessage.value = ''
  giftOpen.value = true
}

async function sendFriendGift(cropItemId: number, quantity: number): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected || giftBusy.value || giftRecipientId.value === 0n) {
    return
  }
  giftBusy.value = true
  giftError.value = ''
  giftMessage.value = ''
  try {
    const response = await socket.sendFriendGift(
      playerId,
      giftRecipientId.value,
      cropItemId,
      quantity,
    )
    setServerClock(response.serverTimeMs)
    if (response.error) {
      throw new Error(describeWsError(response.error))
    }
    if (response.payload.case !== 'sendFriendGiftResponse') {
      throw new Error('赠礼响应无效')
    }
    await acceptMutationResponse(response)
    giftMessage.value = '礼物已发送'
  } catch (error) {
    giftError.value = error instanceof Error ? error.message : String(error)
  } finally {
    giftBusy.value = false
  }
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
    [ErrorCode.INVENTORY_CAPACITY_EXCEEDED]: '仓库空间不足，无法领取全部附件',
    [ErrorCode.ITEM_NOT_OWNED]: '未拥有该物品',
    [ErrorCode.INSUFFICIENT_ITEM_QUANTITY]: '物品数量不足',
    [ErrorCode.PLOT_NOT_FOUND]: '地块不存在',
    [ErrorCode.PLOT_STATE_CONFLICT]: '当前地块状态不能执行此操作',
    [ErrorCode.FERTILIZER_ALREADY_ACTIVE]: '肥料效果仍在生效',
    [ErrorCode.CROP_NOT_MATURE]: '作物尚未成熟',
    [ErrorCode.CHAPTER_NOT_CLAIMABLE]: '章节任务尚未完成',
    [ErrorCode.CHAPTER_REWARD_ALREADY_CLAIMED]: '章节奖励已经领取',
    [ErrorCode.FRIEND_CODE_NOT_FOUND]: '好友码不存在',
    [ErrorCode.FRIEND_CODE_EXPIRED]: '好友码已过期',
    [ErrorCode.CANNOT_FRIEND_SELF]: '不能添加自己为好友',
    [ErrorCode.FRIEND_LIMIT_REACHED]: '好友数量已达上限',
    [ErrorCode.NOT_MUTUAL_FRIEND]: '双方不是互相好友',
    [ErrorCode.VISIT_NOT_FOUND]: '访问会话已失效',
    [ErrorCode.VISIT_EXPIRED]: '访问会话已过期',
    [ErrorCode.STEAL_NOT_AVAILABLE]: '该地块当前不能偷取',
    [ErrorCode.INTERACTION_OUTCOME_UNKNOWN]: '互动结果未知，请稍后重试',
    [ErrorCode.PLOT_NOT_ELIGIBLE]: '该地块当前不能执行此操作',
    [ErrorCode.PEST_ALREADY_PRESENT]: '地块上已有害虫',
    [ErrorCode.PEST_SOURCE_FORBIDDEN]: '不能捉自己投下的虫',
    [ErrorCode.INSUFFICIENT_ACTION_CHANCE]: '互动机会不足',
    [ErrorCode.PET_NOT_FOUND]: '宠物不存在',
    [ErrorCode.PET_ALREADY_OWNED]: '已经拥有该宠物',
    [ErrorCode.PET_NOT_OWNED]: '尚未拥有该宠物',
    [ErrorCode.PET_DISABLED]: '该宠物暂不可用',
  }
  return labels[error.code] ?? `WebSocket 错误 ${error.code}`
}

async function loadFriends(): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || friendsBusy.value) {
    return
  }
  if (!socket.connected) {
    friendsError.value = '尚未连接，无法刷新好友列表'
    return
  }
  friendsBusy.value = true
  friendsError.value = ''
  try {
    const response = await socket.listFriends(playerId)
    setServerClock(response.serverTimeMs)
    if (response.error) {
      throw new Error(describeWsError(response.error))
    }
    if (response.payload.case !== 'listFriendsResponse') {
      throw new Error('好友列表响应 payload 无效')
    }
    friends.value = response.payload.value.friends
  } catch (error) {
    friendsError.value = error instanceof Error ? error.message : String(error)
  } finally {
    friendsBusy.value = false
  }
}

async function generateFriendCode(): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected || friendsBusy.value) {
    return
  }
  friendsBusy.value = true
  friendsError.value = ''
  try {
    const response = await socket.createFriendCode(playerId)
    setServerClock(response.serverTimeMs)
    if (response.error) {
      throw new Error(describeWsError(response.error))
    }
    if (response.payload.case !== 'createFriendCodeResponse') {
      throw new Error('生成好友码响应 payload 无效')
    }
    generatedFriendCode.value = response.payload.value
  } catch (error) {
    friendsError.value = error instanceof Error ? error.message : String(error)
  } finally {
    friendsBusy.value = false
  }
}

async function redeemFriendCode(rawCode: string, options?: { fromInvite?: boolean }): Promise<void> {
  const playerId = session.value?.playerId
  const code = rawCode.trim()
  if (!playerId || !socket.connected || !code || redeemBusy.value || autoRedeemBusy.value) {
    return
  }
  const fromInvite = options?.fromInvite === true
  if (fromInvite) {
    autoRedeemBusy.value = true
  } else {
    redeemBusy.value = true
  }
  redeemMessage.value = ''
  redeemError.value = ''
  try {
    const response = await socket.redeemFriendCode(playerId, code)
    setServerClock(response.serverTimeMs)
    if (response.error) {
      throw new Error(describeWsError(response.error))
    }
    if (response.payload.case !== 'redeemFriendCodeResponse') {
      throw new Error('兑换好友码响应 payload 无效')
    }
    redeemMessage.value = response.payload.value.newlyCreated
      ? `已添加好友：${response.payload.value.friend?.accountName ?? ''}`
      : '早已是好友。'
    if (fromInvite) {
      inviteNotice.value = redeemMessage.value
      clearPendingFriendCode()
      activePanel.value = 'friends'
    }
    await loadFriends()
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    redeemError.value = message
    if (fromInvite) {
      inviteNotice.value = message
      // Terminal invite failures clear the pending code so the player is not
      // stuck retrying forever; transient network errors keep it for reconnect.
      if (isTerminalFriendRedeemFailure(message)) {
        clearPendingFriendCode()
      }
    }
  } finally {
    redeemBusy.value = false
    autoRedeemBusy.value = false
  }
}

function isTerminalFriendRedeemFailure(message: string): boolean {
  return (
    message.includes('好友码不存在') ||
    message.includes('好友码已过期') ||
    message.includes('不能添加自己') ||
    message.includes('好友数量已达上限') ||
    message.includes('请求参数无效')
  )
}

async function redeemPendingFriendInvite(): Promise<void> {
  const code = loadPendingFriendCode()
  if (!code || !socket.connected || autoRedeemBusy.value) {
    return
  }
  await redeemFriendCode(code, { fromInvite: true })
}

function stopVisitHeartbeat(): void {
  if (heartbeatTimer) {
    clearInterval(heartbeatTimer)
    heartbeatTimer = undefined
  }
}

function rememberPendingVisit(playerId: bigint, ownerId: bigint): void {
  try {
    sessionStorage.setItem(
      PENDING_VISIT_STORAGE_KEY,
      JSON.stringify({
        playerId: playerId.toString(),
        ownerId: ownerId.toString(),
      }),
    )
  } catch {
    // Private mode / quota failures must not break enterFriendFarm itself.
  }
}

function forgetPendingVisit(): void {
  try {
    sessionStorage.removeItem(PENDING_VISIT_STORAGE_KEY)
  } catch {
    // Same tolerance as rememberPendingVisit.
  }
}

function markTabSession(playerId: bigint): void {
  try {
    sessionStorage.setItem(TAB_SESSION_STORAGE_KEY, playerId.toString())
  } catch {
    // Without the marker, the next reload will treat this as a fresh entry
    // and force re-login — safer than silently auto-resuming forever.
  }
}

function clearTabSession(): void {
  try {
    sessionStorage.removeItem(TAB_SESSION_STORAGE_KEY)
  } catch {
    // Same tolerance as markTabSession.
  }
}

function loadTabSessionPlayerId(): bigint | undefined {
  try {
    const raw = sessionStorage.getItem(TAB_SESSION_STORAGE_KEY)
    if (!raw) {
      return undefined
    }
    return BigInt(raw)
  } catch {
    clearTabSession()
    return undefined
  }
}

function loadPendingVisitOwner(playerId: bigint): bigint | undefined {
  try {
    const raw = sessionStorage.getItem(PENDING_VISIT_STORAGE_KEY)
    if (!raw) {
      return undefined
    }
    const parsed = JSON.parse(raw) as { playerId?: string; ownerId?: string }
    if (parsed.playerId !== playerId.toString() || !parsed.ownerId) {
      forgetPendingVisit()
      return undefined
    }
    return BigInt(parsed.ownerId)
  } catch {
    forgetPendingVisit()
    return undefined
  }
}

function clearVisit(): void {
  stopVisitHeartbeat()
  forgetPendingVisit()
  visitOwnerId.value = undefined
  visitId.value = undefined
  visitSnapshot.value = undefined
  stealBusyPlotId.value = undefined
  stealError.value = ''
  stealMessage.value = ''
  clearVisitFloats()
}

async function sendVisitHeartbeat(): Promise<void> {
  const playerId = session.value?.playerId
  const ownerId = visitOwnerId.value
  const id = visitId.value
  if (!playerId || !ownerId || !id || !socket.connected) {
    return
  }
  try {
    const response = await socket.farmHeartbeat(playerId, ownerId, id)
    setServerClock(response.serverTimeMs)
    if (response.error) {
      if (response.error.code === ErrorCode.VISIT_NOT_FOUND) {
        // The owner's Zone lost the in-memory visitor lease (e.g. restart
        // or Shard migration); silently re-acquire it instead of bouncing
        // the visitor back to the friends list.
        clearVisit()
        await enterFriendFarm(ownerId)
        return
      }
      throw new Error(describeWsError(response.error))
    }
  } catch (error) {
    visitError.value = error instanceof Error ? error.message : String(error)
  }
}

function startVisitHeartbeat(): void {
  stopVisitHeartbeat()
  heartbeatTimer = setInterval(() => {
    void sendVisitHeartbeat()
  }, HEARTBEAT_INTERVAL_MS)
}

async function enterFriendFarm(ownerId: bigint): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected || visitBusy.value) {
    return
  }
  clearFriendFarmRedDot(ownerId)
  if (visitOwnerId.value !== undefined && visitOwnerId.value !== ownerId) {
    // A visitor may only be inside one friend's farm at a time; leave the
    // previous farm before entering the newly selected one.
    await exitFriendFarm()
  }
  visitBusy.value = true
  visitError.value = ''
  try {
    const response = await socket.enterFriendFarm(playerId, ownerId)
    setServerClock(response.serverTimeMs)
    if (response.error) {
      throw new Error(describeWsError(response.error))
    }
    if (response.payload.case !== 'enterFriendFarmResponse') {
      throw new Error('进入好友农场响应 payload 无效')
    }
    visitOwnerId.value = ownerId
    visitId.value = response.payload.value.visitId
    visitSnapshot.value = response.payload.value.snapshot
    rememberPendingVisit(playerId, ownerId)
    startVisitHeartbeat()
  } catch (error) {
    visitError.value = error instanceof Error ? error.message : String(error)
    forgetPendingVisit()
  } finally {
    visitBusy.value = false
  }
}

async function exitFriendFarm(): Promise<void> {
  const playerId = session.value?.playerId
  const ownerId = visitOwnerId.value
  const id = visitId.value
  if (!playerId || !ownerId || !id || visitBusy.value) {
    clearVisit()
    return
  }
  visitBusy.value = true
  try {
    if (socket.connected) {
      const response = await socket.exitFriendFarm(playerId, ownerId, id)
      if (response.error && response.error.code !== ErrorCode.VISIT_NOT_FOUND) {
        visitError.value = describeWsError(response.error)
      }
    }
  } catch (error) {
    visitError.value = error instanceof Error ? error.message : String(error)
  } finally {
    clearVisit()
    visitBusy.value = false
  }
}

const DEVELOPMENT_PEST_ID = 1

// runFriendAction is shared by steal / apply-pest / catch-pest / help-clean:
// apply visitor_patch from the RPC response (no state_version on
// FriendActionResponse — see stealFriendCrop comment), ignore farm_patch
// (FARM_VIEW_CHANGED push owns that), and surface a short notice.
async function runFriendAction(
  plotId: number,
  action: 'steal' | 'pest' | 'catch' | 'clean',
): Promise<void> {
  const playerId = session.value?.playerId
  const ownerId = visitOwnerId.value
  const id = visitId.value
  if (!playerId || !ownerId || !id || !socket.connected || stealBusyPlotId.value !== undefined) {
    return
  }
  stealBusyPlotId.value = plotId
  stealError.value = ''
  stealMessage.value = ''
  const coinsBefore = Number(snapshot.value?.coinBalance ?? 0n)
  try {
    let response: WsEnvelope
    let expectedCase:
      | 'stealFriendCropResponse'
      | 'applyPestToFriendResponse'
      | 'catchPestForFriendResponse'
      | 'helpCleanFriendPlotResponse'
    let successMessage: string
    let floatText: string
    let floatTone: 'success' | 'error' = 'success'
    switch (action) {
      case 'steal': {
        const plot = visitSnapshot.value?.plots.find((entry) => entry.plotId === plotId)
        const version = visitSnapshot.value?.version
        if (!plot?.cropItemId || !version?.farmViewEpoch?.length) {
          throw new Error('好友农场视图缺少作物或版本，请刷新后重试')
        }
        const cropName =
          cropCatalog.value.find((entry) => entry.cropItemId === plot.cropItemId)?.name ||
          `作物#${plot.cropItemId}`
        response = await socket.stealFriendCrop(
          playerId,
          ownerId,
          id,
          plotId,
          plot.cropItemId,
          version.farmViewEpoch,
          version.farmViewSeq,
        )
        expectedCase = 'stealFriendCropResponse'
        successMessage = `偷菜成功，获得 ${cropName} ×1。`
        floatText = `+${cropName} ×1`
        break
      }
      case 'pest':
        response = await socket.applyPestToFriend(
          playerId,
          ownerId,
          id,
          plotId,
          DEVELOPMENT_PEST_ID,
        )
        expectedCase = 'applyPestToFriendResponse'
        successMessage = '投虫成功。'
        floatText = '投虫成功'
        break
      case 'catch':
        response = await socket.catchPestForFriend(playerId, ownerId, id, plotId)
        expectedCase = 'catchPestForFriendResponse'
        successMessage = '捉虫成功。'
        floatText = '捉虫成功'
        break
      case 'clean':
        response = await socket.helpCleanFriendPlot(playerId, ownerId, id, plotId)
        expectedCase = 'helpCleanFriendPlotResponse'
        successMessage = '已帮好友清理地块。'
        floatText = '已清理'
        break
    }
    setServerClock(response.serverTimeMs)
    if (response.error) {
      if (
        action === 'steal' &&
        (response.error.code === ErrorCode.STEAL_NOT_AVAILABLE ||
          response.error.code === ErrorCode.REQUEST_ID_CONFLICT)
      ) {
        void enterFriendFarm(ownerId)
      }
      throw new Error(describeWsError(response.error))
    }
    if (response.payload.case !== expectedCase) {
      throw new Error('好友互动响应 payload 无效')
    }
    const currentSnapshot = snapshot.value
    const patch = response.payload.value.visitorPatch
    if (currentSnapshot && patch) {
      snapshot.value = applyPatch(currentSnapshot, patch)
    }
    const coinsAfter = Number(snapshot.value?.coinBalance ?? coinsBefore)
    const coinDelta = coinsAfter - coinsBefore

    if (action === 'steal') {
      const guard = response.payload.value.stealGuard
      if (guard?.guardTriggered) {
        const penalty = Number(guard.guardPenaltyApplied ?? 0n)
        if (penalty > 0) {
          successMessage = `${successMessage} 但你偷菜被狗咬了，被罚款 ${penalty} 金币。`
          pushPetFloat(`你被狗咬了，-${penalty}金币`)
          pushVisitFloat(plotId, `你被狗咬了，-${penalty}金币`, 'error')
        }
      }
      pushVisitFloat(plotId, floatText)
    } else if (action === 'catch' || action === 'clean') {
      floatText = '做好事奖励你+1金币'
      successMessage = `${successMessage} ${floatText}`
      pushVisitFloat(plotId, floatText)
    } else if (action === 'pest') {
      if (coinDelta >= 0) {
        floatText = `风险与机遇并存，+${coinDelta}金币`
        floatTone = 'success'
      } else {
        floatText = `你真是太坏了${coinDelta}金币。`
        floatTone = 'error'
      }
      successMessage = `${successMessage} ${floatText}`
      pushVisitFloat(plotId, floatText, floatTone)
    } else {
      pushVisitFloat(plotId, floatText)
    }
    stealMessage.value = successMessage
  } catch (error) {
    stealError.value = error instanceof Error ? error.message : String(error)
    pushVisitFloat(plotId, '操作失败', 'error')
  } finally {
    stealBusyPlotId.value = undefined
  }
}

function pushPetFloat(text: string): void {
  petFloatText.value = text
  if (petFloatTimer !== undefined) {
    clearTimeout(petFloatTimer)
  }
  petFloatTimer = setTimeout(() => {
    petFloatText.value = ''
    petFloatTimer = undefined
  }, 1800)
}

function stealFriendCrop(plotId: number): Promise<void> {
  return runFriendAction(plotId, 'steal')
}

function applyPestToFriend(plotId: number): Promise<void> {
  return runFriendAction(plotId, 'pest')
}

function catchPestForFriend(plotId: number): Promise<void> {
  return runFriendAction(plotId, 'catch')
}

function helpCleanFriendPlot(plotId: number): Promise<void> {
  return runFriendAction(plotId, 'clean')
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
  const patch = mutationResponsePatch(response)
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
    case 'buy-fertilizer':
      return `已购买 ${response.payload.case === 'buyFertilizerResponse' ? response.payload.value.quantity : 0} 袋肥料。`
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
    case 'catch':
      return '捉虫成功，作物继续成长。'
  }
}

function farmActionFloatText(action: FarmAction, response: WsEnvelope): string {
  switch (action) {
    case 'plant':
      return '已种下'
    case 'fertilize':
      return '施肥成功'
    case 'harvest':
      return `+${response.payload.case === 'harvestResponse' ? response.payload.value.harvestedQuantity : 0} 个`
    case 'clean':
      return '已清理'
    case 'catch':
      return '捉虫成功'
    default:
      return ''
  }
}

async function runFarmAction(request: FarmActionRequest): Promise<void> {
  const playerId = session.value?.playerId
  if (!playerId || !socket.connected || busyAction.value) {
    return
  }
  const { action, plotId } = request
  if (
    (action === 'plant' ||
      action === 'fertilize' ||
      action === 'harvest' ||
      action === 'clean' ||
      action === 'catch') &&
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
        const quote =
          (request.shopEntryId
            ? shopEntries.value.find((entry) => entry.shopEntryId === request.shopEntryId)
            : undefined) ??
          (request.seedItemId
            ? shopEntries.value.find((entry) => entry.itemId === request.seedItemId)
            : undefined)
        if (!quote) throw new Error('种子报价尚未加载')
        response = await socket.buySeeds(
          playerId,
          quote.shopEntryId,
          request.quantity ?? 1,
          request.priceVersion ?? quote.priceVersion,
        )
        break
      }
      case 'buy-fertilizer': {
        const quote = shopEntries.value.find((entry) => entry.itemId === 1)
        if (!quote) throw new Error('肥料报价尚未加载')
        response = await socket.buyFertilizer(
          playerId,
          quote.shopEntryId,
          request.quantity ?? 1,
          quote.priceVersion,
        )
        break
      }
      case 'plant': {
        const seedItemId = request.seedItemId
        if (!seedItemId) {
          throw new Error('未选择种子')
        }
        response = await socket.plant(playerId, plotId!, seedItemId)
        break
      }
      case 'fertilize':
        response = await socket.applyFertilizer(playerId, plotId!, 1)
        break
      case 'harvest':
        response = await socket.harvest(playerId, plotId!)
        break
      case 'sell': {
        const cropItemId = request.cropItemId
        if (!cropItemId) {
          throw new Error('未选择出售作物')
        }
        const priceVersion =
          request.priceVersion ??
          cropCatalog.value.find((crop) => crop.cropItemId === cropItemId)?.sellPriceVersion
        if (priceVersion === undefined) {
          throw new Error('作物收购报价尚未加载')
        }
        response = request.sellAll
          ? await socket.sellAll(playerId, cropItemId, priceVersion)
          : await socket.sellQuantity(
              playerId,
              cropItemId,
              request.quantity ?? 1,
              priceVersion,
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
      case 'catch':
        response = await socket.catchPest(playerId, plotId!)
        break
    }
    await acceptMutationResponse(response)
    actionMessage.value = actionSuccessMessage(action, response)
    if (plotId) {
      pushFarmFloat(plotId, farmActionFloatText(action, response))
    }
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : String(error)
    if (plotId) {
      pushFarmFloat(plotId, '操作失败', 'error')
    }
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

function handleFarmPresenceChanged(envelope: WsEnvelope): void {
  if (envelope.payload.case !== 'farmPresenceChangedPush') {
    return
  }
  const push = envelope.payload.value
  const visitorID = push.visitorPlayerId
  const visitorKey = visitorID?.toString() || push.visitorAccountName || 'unknown'
  const who = farmVisitorLabel(visitorID, push.visitorAccountName)

  if (push.kind === FarmPresenceKind.FARM_CROP_STOLEN && push.plotId) {
    const cropName =
      cropCatalog.value.find((entry) => entry.cropItemId === push.cropItemId)?.name ||
      `作物#${push.cropItemId ?? 0}`
    const quantity = push.quantity ?? 1
    pushFarmFloat(
      push.plotId,
      push.guardTriggered
        ? `${who}偷了${quantity}个${cropName}，被狗咬了扣除金币`
        : `${who}偷了${quantity}个${cropName}`,
      push.guardTriggered ? 'error' : 'success',
    )
    return
  }

  const next = new Map(farmVisitorEntries.value)
  if (push.kind === FarmPresenceKind.FARM_VISITOR_ENTERED) {
    next.set(visitorKey, {
      playerId: visitorID,
      accountName: push.visitorAccountName,
    })
  } else if (push.kind === FarmPresenceKind.FARM_VISITOR_LEFT) {
    next.delete(visitorKey)
  }
  farmVisitorEntries.value = next
}

function handleRedDotChanged(envelope: WsEnvelope): void {
  if (envelope.payload.case !== 'redDotChangedPush') {
    return
  }
  const push = envelope.payload.value
  // protobuf-es strips the RED_DOT_CATEGORY_ / RED_DOT_OPERATION_ prefixes from
  // TypeScript enum members (MAIL / SET), unlike the Go / proto identifiers.
  if (
    push.category !== RedDotCategory.MAIL &&
    push.category !== RedDotCategory.FRIEND_FARM
  ) {
    console.warn('忽略未知红点 category', push.category)
    return
  }
  const set = push.operation === RedDotOperation.SET
  const clear = push.operation === RedDotOperation.CLEAR
  if (!set && !clear) {
    console.warn('忽略未知红点 operation', push.operation)
    return
  }
  if (push.category === RedDotCategory.MAIL) {
    mailRedDot.value = set
    return
  }
  const ownerId = push.sourcePlayerId
  if (!ownerId) {
    console.warn('好友农场红点缺少 source_player_id')
    return
  }
  const key = ownerId.toString()
  const next = new Set(friendFarmRedDots.value)
  if (set) {
    next.add(key)
  } else {
    next.delete(key)
  }
  friendFarmRedDots.value = next
}

// handleFarmViewChanged applies an incremental FarmViewPatch to the friend
// farm currently being visited. The public farm version is independent from
// the visitor's own (owner_epoch, player_seq): an epoch change means the
// owner Actor rotated (restart/migration/re-created), so the only correct
// recovery is a fresh ENTER_FRIEND_FARM full snapshot; a seq gap (missed or
// out-of-order Push) recovers the same way, while a same-or-older seq is a
// harmless duplicate that is simply ignored.
function handleFarmViewChanged(envelope: WsEnvelope): void {
  if (envelope.payload.case !== 'farmViewChangedPush') {
    return
  }
  const patch = envelope.payload.value
  if (patch.ownerPlayerId === session.value?.playerId) {
    refreshOwnFarmOnVisitorChange(patch)
    return
  }
  const ownerId = visitOwnerId.value
  if (ownerId === undefined || patch.ownerPlayerId !== ownerId) {
    return
  }
  const currentSnapshot = visitSnapshot.value
  const currentVersion = currentSnapshot?.version
  const nextVersion = patch.version
  const decision = decideFarmViewPatch({
    hasCurrentSnapshot: Boolean(currentSnapshot),
    currentEpoch: currentVersion?.farmViewEpoch,
    currentSeq: currentVersion?.farmViewSeq,
    nextEpoch: nextVersion?.farmViewEpoch,
    nextSeq: nextVersion?.farmViewSeq,
  })
  if (decision.action === 'resync') {
    void enterFriendFarm(ownerId)
    return
  }
  if (decision.action === 'ignore' || !currentSnapshot) {
    return
  }
  visitSnapshot.value = applyFarmViewPatch(currentSnapshot, patch)
}

// refreshOwnFarmOnVisitorChange handles the FarmViewPatch the owner receives
// for their own farm (farmview.Broadcaster always includes the owner). Most
// of those merely echo a change this client already applied from its own
// command response, so re-fetching on every one of them would double the
// traffic of every plot action. A visitor-driven change is the case with no
// other push to carry it: FriendActionResponse patches the visitor only, and
// the owner's own player_seq stream says nothing about the stolen quantity /
// pest effect. Comparing the public projection against the local plots
// isolates exactly that case. Pest presence is flipped immediately so the
// owner sees "有虫" without waiting for the full snapshot round-trip; other
// divergences still recover via GET_PLAYER_SNAPSHOT.
function refreshOwnFarmOnVisitorChange(patch: FarmViewPatch): void {
  const current = snapshot.value
  if (!current) {
    return
  }
  let diverged = false
  let pestFlipped = false
  const nextPlots = current.plots.map((local) => {
    const publicPlot = patch.plotUpserts.find((plot) => plot.plotId === local.plotId)
    if (!publicPlot) {
      return local
    }
    if (
      local.plotState !== publicPlot.plotState ||
      local.harvestableQuantity !== publicPlot.harvestableQuantity
    ) {
      diverged = true
    }
    if (Boolean(local.pestEffect) === publicPlot.pestActive) {
      return local
    }
    diverged = true
    pestFlipped = true
    if (publicPlot.pestActive) {
      return {
        ...local,
        pestEffect:
          local.pestEffect ??
          create(EffectViewSchema, {
            effectInstanceId: 'pending',
            effectItemId: 1,
            effectConfigVersion: 0n,
            startAtMs: 0n,
            endAtMs: 0n,
          }),
      }
    }
    return { ...local, pestEffect: undefined }
  })
  if (pestFlipped) {
    snapshot.value = { ...current, plots: nextPlots }
  }
  if (diverged) {
    void recoverSnapshotGap()
  }
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
  phase.value = 'ready'
  // The snapshot alone is enough to show the farm, so a failing side load must
  // not throw: that would drop the player into the shell with an empty seed bar
  // and an error nobody can see.
  await reloadCatalog()
  await refreshPetPanel()
  await loadFriends()
  await refreshMailRedDot(connection.auth.playerId)
  await redeemPendingFriendInvite()
  // Reload restores only the Session cookie; visit leases die with the old
  // WebSocket. If this tab was mid-visit, re-ENTER the remembered owner so
  // the friend-farm dashboard comes back instead of the player's own farm.
  const pendingOwnerId = loadPendingVisitOwner(connection.auth.playerId)
  if (pendingOwnerId !== undefined) {
    await enterFriendFarm(pendingOwnerId)
  }
}

function httpErrorCode(error: unknown): HttpErrorCode | undefined {
  return error instanceof ProtobufHttpError ? error.code : undefined
}

function describeAuthError(error: unknown): string {
  switch (httpErrorCode(error)) {
    case HttpErrorCode.INVALID_CREDENTIALS:
      return '密码错误'
    case HttpErrorCode.INVALID_ARGUMENT:
      return '账号需为 3-32 位小写字母、数字或下划线，密码至少 6 位'
    case HttpErrorCode.RATE_LIMITED:
      return '尝试过于频繁，请稍后再试'
    case HttpErrorCode.CSRF_REJECTED:
      return '安全校验失败，请刷新页面后重试'
    default:
      return error instanceof Error ? error.message : String(error)
  }
}

// One form, no mode switch: an unknown account is registered on the spot. The
// server cannot tell us "account does not exist" (login answers
// INVALID_CREDENTIALS for both a wrong password and a missing account), so a
// failed login is retried as a register, and a taken account name proves the
// account existed and the password was simply wrong.
async function authenticateOrRegister(): Promise<SessionView> {
  try {
    return await authenticate('login', accountName.value, password.value, csrfToken.value)
  } catch (error) {
    if (httpErrorCode(error) !== HttpErrorCode.INVALID_CREDENTIALS) {
      throw error
    }
    try {
      return await authenticate('register', accountName.value, password.value, csrfToken.value)
    } catch (registerError) {
      if (httpErrorCode(registerError) === HttpErrorCode.ACCOUNT_NAME_UNAVAILABLE) {
        throw new Error('密码错误')
      }
      throw new Error(describeAuthError(registerError))
    }
  }
}

async function submitCredentials(): Promise<void> {
  if (busy.value) {
    return
  }
  busy.value = true
  clearResult()
  try {
    phase.value = 'csrf'
    csrfToken.value = (await fetchCsrf()).csrfToken
    phase.value = 'session'
    session.value = await authenticateOrRegister()
    password.value = ''

    // Successful registration/login rotates the token; never reuse the pre-auth value.
    csrfToken.value = (await fetchCsrf()).csrfToken
    await establishSnapshot()
    if (session.value) {
      markTabSession(session.value.playerId)
    }
  } catch (error) {
    phase.value = 'failed'
    errorMessage.value = describeAuthError(error)
  } finally {
    busy.value = false
  }
}

async function reconnect(): Promise<void> {
  busy.value = true
  try {
    await establishSnapshot()
    if (session.value) {
      markTabSession(session.value.playerId)
    }
  } catch (error) {
    phase.value = 'failed'
    errorMessage.value = error instanceof Error ? error.message : String(error)
  } finally {
    busy.value = false
  }
}

// dropConnection only closes the realtime link, keeping the HTTP Session so
// 重新连接 can re-issue a Ticket without another password round trip.
async function dropConnection(): Promise<void> {
  stopVisitHeartbeat()
  if (visitOwnerId.value !== undefined) {
    await exitFriendFarm()
  }
  socket.disconnect()
  phase.value = 'disconnected'
}

async function signOut(): Promise<void> {
  stopVisitHeartbeat()
  if (visitOwnerId.value !== undefined) {
    await exitFriendFarm()
  }
  socket.disconnect()
  clearTabSession()
  forgetPendingVisit()
  try {
    const token = csrfToken.value || (await fetchCsrf()).csrfToken
    await logout(token)
  } catch {
    // Cookie may already be gone; still tear down local state below.
  }
  session.value = undefined
  snapshot.value = undefined
  stateVersion.value = undefined
  activePanel.value = null
  password.value = ''
  phase.value = 'idle'
}

// resumeAfterReload only runs when this tab already marked itself active in
// sessionStorage. That marker survives F5 but is wiped when the tab closes,
// so a later re-open of the site does not silently reuse the HttpOnly cookie.
async function resumeAfterReload(): Promise<void> {
  if (busy.value) {
    return
  }
  const tabPlayerId = loadTabSessionPlayerId()
  if (tabPlayerId === undefined) {
    await endOrphanedCookieSession()
    return
  }
  busy.value = true
  try {
    const existing = await fetchSession()
    if (!existing || existing.playerId !== tabPlayerId) {
      clearTabSession()
      forgetPendingVisit()
      return
    }
    session.value = existing
    accountName.value = existing.accountName
    await establishSnapshot()
    markTabSession(existing.playerId)
  } catch (error) {
    phase.value = 'failed'
    errorMessage.value = error instanceof Error ? error.message : String(error)
  } finally {
    busy.value = false
  }
}

// endOrphanedCookieSession clears a leftover HttpOnly Session after the tab
// was closed (sessionStorage gone) so "open the site again" is truly logged out,
// not just missing an auto-resume.
async function endOrphanedCookieSession(): Promise<void> {
  try {
    const existing = await fetchSession()
    if (!existing) {
      return
    }
    const token = (await fetchCsrf()).csrfToken
    await logout(token)
  } catch {
    // First visit or already logged out — leave the login form idle.
  }
}

function tearDownRealtime(): void {
  stopVisitHeartbeat()
  socket.disconnect()
}

onMounted(() => {
  const inviteCode = captureInviteFriendCodeFromLocation()
  if (inviteCode) {
    inviteNotice.value = '登录或注册后将自动添加好友'
  } else if (loadPendingFriendCode()) {
    inviteNotice.value = '登录或注册后将自动添加好友'
  }
  clockTimer = setInterval(() => {
    nowMs.value = BigInt(Date.now()) + serverClockOffsetMs
  }, 1000)
  // pagehide fires for both refresh and tab close. WebSocket always dies with
  // the document; we only tear it down here. We deliberately do NOT logout or
  // clear sessionStorage: refresh must keep both so resumeAfterReload can run.
  window.addEventListener('pagehide', tearDownRealtime)
  void resumeAfterReload()
})

onBeforeUnmount(() => {
  window.removeEventListener('pagehide', tearDownRealtime)
  if (clockTimer) {
    clearInterval(clockTimer)
  }
  tearDownRealtime()
})
</script>

<template>
  <main v-if="!signedIn" class="login-shell">
    <section class="login-card" aria-labelledby="login-title">
      <h1 id="login-title" class="login-title">Grow!</h1>

      <form class="login-form" @submit.prevent="submitCredentials">
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
            autocomplete="current-password"
            type="password"
            minlength="6"
            maxlength="128"
            placeholder="至少 6 个字符"
            required
          />
        </label>
        <p v-if="inviteNotice && !signedIn" class="login-invite-hint" role="status">
          {{ inviteNotice }}
        </p>
        <button class="primary" type="submit" :disabled="busy">
          {{ busy ? '处理中…' : '进入农场' }}
        </button>
      </form>

      <p v-if="errorMessage" class="error-banner" role="alert">{{ errorMessage }}</p>
    </section>
  </main>

  <main v-else class="game-shell">
    <TopNav
      :account-name="session?.accountName ?? ''"
      :coin-balance="snapshot?.coinBalance"
      :active-panel="activePanel"
      :mail-red-dot="mailRedDot"
      :friend-red-dot="friendRedDot"
      @select="togglePanel"
    />

    <p v-if="shellNotice" class="shell-notice" role="status">
      <span>{{ shellNotice }}</span>
      <button type="button" :disabled="!canConnect" @click="reconnect">
        {{ busy ? '连接中…' : '重新连接' }}
      </button>
    </p>

    <p v-if="inviteNotice && signedIn" class="shell-notice" role="status">
      <span>{{ inviteNotice }}</span>
    </p>

    <FriendFarmDashboard
      v-if="visiting"
      :snapshot="visitSnapshot"
      :owner-label="visitOwnerLabel"
      :crop-catalog="cropCatalog"
      :connected="connected"
      :busy="visitBusy"
      :steal-busy-plot-id="stealBusyPlotId"
      :now-ms="nowMs"
      :plot-floats="visitFloats"
      @steal="stealFriendCrop"
      @pest="applyPestToFriend"
      @catch="catchPestForFriend"
      @clean="helpCleanFriendPlot"
      @plot-feedback="(plotId, text) => pushVisitFloat(plotId, text, 'error')"
      @exit="exitFriendFarm"
      @open-profile="profileOpen = true"
    />
    <FarmDashboard
      v-else
      :snapshot="snapshot"
      :crop-catalog="cropCatalog"
      :connected="connected"
      :busy-action="busyAction"
      :now-ms="nowMs"
      :active-pet="activePet"
      :plot-floats="farmFloats"
      :visitors="farmVisitors"
      @action="runFarmAction"
      @plot-feedback="(plotId, text) => pushFarmFloat(plotId, text, 'error')"
      @open-shop="togglePanel('shop')"
      @open-pet="togglePanel('pet')"
      @reload-catalog="reloadCatalog"
    />

    <GameDrawer
      :open="activePanel === 'account'"
      :title="panelTitles.account"
      :kicker="panelKickers.account"
      @close="activePanel = null"
    >
      <AccountPanel
        :account-name="session?.accountName ?? ''"
        :player-id="session?.playerId.toString() ?? ''"
        :phase-label="phaseLabels[phase]"
        :connected="connected"
        :busy="busy"
        :can-reconnect="canConnect"
        :error-message="errorMessage"
        :steps="timelineSteps"
        :facts="diagnosticFacts"
        @reconnect="reconnect"
        @disconnect="dropConnection"
        @logout="signOut"
      />
    </GameDrawer>

    <GameDrawer
      :open="activePanel === 'shop'"
      :title="panelTitles.shop"
      :kicker="panelKickers.shop"
      @close="activePanel = null"
    >
      <ShopPanel
        :shop-entries="shopEntries"
        :crop-catalog="cropCatalog"
        :inventory="inventoryMap"
        :coin-balance="snapshot?.coinBalance"
        :connected="connected"
        :busy-action="busyAction"
        @action="runFarmAction"
      />
    </GameDrawer>

    <GameDrawer
      :open="activePanel === 'pet'"
      :title="panelTitles.pet"
      :kicker="panelKickers.pet"
      @close="activePanel = null"
    >
      <PetPanel
        :panel="petPanel"
        :now-ms="nowMs"
        :busy-buy-pet-id="petBusyBuyId"
        :busy-deploy-pet-id="petBusyDeployId"
        :busy-buy-food="petBusyBuyFood"
        :busy-feed="petBusyFeed"
        :error="petError"
        :message="petMessage"
        :float-text="petFloatText"
        @buy-pet="buyPet"
        @deploy-pet="deployPet"
        @buy-food="buyPetFood"
        @feed="feedPet"
        @refresh="refreshPetPanel"
      />
    </GameDrawer>

    <GameDrawer
      :open="activePanel === 'compendium'"
      :title="panelTitles.compendium"
      :kicker="panelKickers.compendium"
      @close="activePanel = null"
    >
      <CompendiumPanel
        :career="snapshot?.career"
        :catalog="cropCatalog"
        :compendium="snapshot?.cropCompendium"
      />
    </GameDrawer>

    <GameDrawer
      :open="activePanel === 'friends'"
      :title="panelTitles.friends"
      :kicker="panelKickers.friends"
      @close="activePanel = null"
    >
      <FriendsPanel
        :friends="friends"
        :connected="connected"
        :busy="friendsBusy"
        :error="friendsError"
        :generated-code="generatedFriendCode?.code ?? ''"
        :share-url="generatedFriendCode?.shareUrl ?? ''"
        :redeem-busy="redeemBusy || autoRedeemBusy"
        :redeem-message="redeemMessage"
        :redeem-error="redeemError"
        :visit-owner-id="visitOwnerId"
        :visit-busy="visitBusy"
        :has-farm-red-dot="friendHasFarmRedDot"
        @refresh="loadFriends"
        @generate="generateFriendCode"
        @redeem="redeemFriendCode"
        @enter="enterFriendFarm"
        @gift="openGiftPanel"
      />
    </GameDrawer>

    <GameDrawer
      :open="activePanel === 'mailbox'"
      :title="panelTitles.mailbox"
      :kicker="panelKickers.mailbox"
      @close="activePanel = null"
    >
      <MailboxPanel
        :open="activePanel === 'mailbox'"
        :mails="mailboxMails"
        :next-page-token="mailboxNextPageToken"
        :filter="mailboxFilter"
        :loading="mailboxLoading"
        :loading-more="mailboxLoadingMore"
        :claiming-mail-id="mailboxClaimingId"
        :error="mailboxError"
        :message="mailboxMessage"
        :item-name="mailItemName"
        @filter="mailboxFilter = $event"
        @refresh="refreshMailbox()"
        @load-more="refreshMailbox(mailboxNextPageToken)"
        @open-mail="markMailRead"
        @claim="claimMail"
      />
    </GameDrawer>

    <GameDrawer
      :open="activePanel === 'tasks'"
      :title="panelTitles.tasks"
      :kicker="panelKickers.tasks"
      @close="activePanel = null"
    >
      <TaskPanel
        :chapter="snapshot?.currentChapter"
        :connected="connected"
        :busy-action="busyAction"
        @action="runFarmAction"
      />
    </GameDrawer>

    <GameDrawer
      :open="activePanel === 'inventory'"
      :title="panelTitles.inventory"
      :kicker="panelKickers.inventory"
      @close="activePanel = null"
    >
      <InventoryPanel
        :shop-entries="shopEntries"
        :crop-catalog="cropCatalog"
        :inventory="inventoryMap"
        :connected="connected"
        :busy-action="busyAction"
        @action="runFarmAction"
      />
    </GameDrawer>

    <PlayerProfileModal
      :open="profileOpen && visiting"
      :title="`${visitOwnerLabel} 的资料`"
      :career="visitSnapshot?.career"
      :catalog="cropCatalog"
      :compendium="visitSnapshot?.cropCompendium"
      :owner-label="visitOwnerLabel"
      @close="profileOpen = false"
    />
    <FriendGiftPanel
      :open="giftOpen"
      :recipient-name="giftRecipientName"
      :recipient-player-id="giftRecipientId"
      :crops="cropCatalog"
      :inventory="inventoryMap"
      :busy="giftBusy"
      :error="giftError"
      :message="giftMessage"
      @close="giftOpen = false"
      @send="sendFriendGift"
    />
  </main>
</template>
