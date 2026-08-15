import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import App from '../App.vue'

// The shell is driven entirely by the HTTP handshake and the WebSocket, so the
// only way to exercise the navigation the way a player does is to fake both and
// then click the real buttons.
const harness = vi.hoisted(() => ({
  playerId: 7n,
  configVersion: 3n,
  configUrl: 'http://127.0.0.1:18080/v1/client-config',
  configSha256: new Uint8Array(32),
  sockets: [] as unknown[],
  friends: [] as Array<Record<string, unknown>>,
  unlockedCropIds: [] as number[],
  unreadMailCount: 0,
}))

vi.mock('../lib/http', () => ({
  ProtobufHttpError: class ProtobufHttpError extends Error {},
  fetchCsrf: async () => ({ csrfToken: 'csrf-token' }),
  fetchSession: async () => undefined,
  authenticate: async () => ({ playerId: harness.playerId, accountName: 'tester' }),
  logout: async () => undefined,
  fetchBootstrap: async () => ({
    authBootstrap: {
      playerId: harness.playerId,
      protocolMin: 1,
      protocolMax: 1,
      clientConfigUrl: harness.configUrl,
      clientConfigSha256: harness.configSha256,
      clientConfigVersion: harness.configVersion,
    },
    gateways: [{ gatewayId: 'local-gateway', websocketUrl: 'ws://127.0.0.1:8081/ws' }],
  }),
  downloadClientConfig: async () => ({
    schemaVersion: 1,
    clientConfigVersion: harness.configVersion,
    publishedAtMs: 0n,
  }),
  selectGateway: (gateways: { gatewayId: string }[]) => gateways[0],
  issueWsTicket: async () => ({ wsTicket: 'ws-ticket' }),
}))

vi.mock('../lib/ws', () => {
  const envelope = (payloadCase: string, value: unknown) => ({
    requestId: 'req-1',
    serverTimeMs: BigInt(Date.now()),
    stateVersion: { ownerEpoch: 1n, playerSeq: 1n },
    error: undefined,
    payload: { case: payloadCase, value },
  })

  const snapshot = {
    playerId: harness.playerId,
    coinBalance: 29n,
    inventory: [{ itemId: 1001, quantity: 2 }],
    plots: [
      { plotId: 1, plotState: 0, cropId: 0, harvestableQuantity: 0 },
      {
        plotId: 2,
        plotState: 1,
        cropId: 2001,
        harvestableQuantity: 0,
        estimatedMatureAtMs: BigInt(Date.now() + 90_000),
      },
    ],
    currentChapter: { chapterId: 1, status: 1, tasks: [] },
    career: {},
    cropCompendium: { unlockedCropIds: [] },
  }

  // Every field below keeps the width the proto declares. uint64 fields arrive
  // as BigInt, and feeding one into Number arithmetic throws mid-render, which
  // kills the whole app: the seed bar formats maturity_seconds on every paint.
  const crops = [
    {
      cropId: 2001,
      name: '胡萝卜',
      seedItemId: 1001,
      cropItemId: 1002,
      seedShopEntryId: 5001,
      seedUnitPrice: 2n,
      seedPriceVersion: 1n,
      sellUnitPrice: 5n,
      sellPriceVersion: 1n,
      maturitySeconds: 100n,
      baseYield: 3,
    },
    {
      cropId: 2002,
      name: '土豆',
      seedItemId: 1003,
      cropItemId: 1004,
      seedShopEntryId: 5002,
      seedUnitPrice: 4n,
      seedPriceVersion: 1n,
      sellUnitPrice: 9n,
      sellPriceVersion: 1n,
      maturitySeconds: 3725n,
      baseYield: 5,
    },
  ]

  class FarmWebSocket {
    connected = false
    private connectionHandler?: (value: boolean) => void
    private redDotChangedHandler?: (envelope: unknown) => void

    constructor() {
      harness.sockets.push(this)
    }

    setConnectionHandler(handler?: (value: boolean) => void) {
      this.connectionHandler = handler
    }

    setPlayerStateChangedHandler() {}
    setFarmPresenceChangedHandler() {}
    setFarmViewChangedHandler() {}
    setRedDotChangedHandler(handler?: (envelope: unknown) => void) {
      this.redDotChangedHandler = handler
    }

    emitMailCount(count: number) {
      this.redDotChangedHandler?.({
        payload: {
          case: 'redDotChangedPush',
          value: { category: 1, operation: 1, count },
        },
      })
    }

    async connectAndAuth(
      _url: string,
      _ticket: string,
      expectedPlayerId: bigint,
      onSocketOpen?: () => void,
    ) {
      this.connected = true
      this.connectionHandler?.(true)
      onSocketOpen?.()
      return {
        auth: {
          playerId: expectedPlayerId,
          heartbeatIntervalMs: 15000,
          clientConfigVersion: harness.configVersion,
          clientConfigUrl: harness.configUrl,
          clientConfigSha256: harness.configSha256,
          protocolMin: 1,
          protocolMax: 1,
        },
        requestId: 'auth-1',
        serverTimeMs: BigInt(Date.now()),
      }
    }

    disconnect() {
      const wasConnected = this.connected
      this.connected = false
      if (wasConnected) {
        this.connectionHandler?.(false)
      }
    }

    async requestPlayerSnapshot() {
      return envelope('getPlayerSnapshotResponse', {
        snapshot: {
          ...snapshot,
          cropCompendium: { unlockedCropIds: [...harness.unlockedCropIds] },
        },
      })
    }

    async requestShop() {
      return envelope('getShopResponse', {
        entries: [{ shopEntryId: 5001, itemId: 1001, unitPrice: 2n, priceVersion: 1n }],
        crops,
      })
    }

    async requestPetPanel() {
      return envelope('getPetPanelResponse', { panel: { ownedPets: [], shopEntries: [] } })
    }

    async listFriends() {
      return envelope('listFriendsResponse', { friends: harness.friends })
    }

    async enterFriendFarm() {
      return envelope('enterFriendFarmResponse', {
        visitId: 'visit-1',
        snapshot: {
          playerId: harness.playerId,
          coinBalance: 29n,
          inventory: [{ itemId: 1001, quantity: 2 }],
          plots: [],
          currentChapter: { chapterId: 1, status: 1, tasks: [] },
          career: {},
          cropCompendium: { unlockedCropIds: [] },
        },
      })
    }

    async getOfflineVisitors() {
      return envelope('getOfflineVisitorsResponse', { visitors: [], visitorVersion: 0n, truncated: false })
    }

    async ackOfflineVisitors() {
      return envelope('ackOfflineVisitorsResponse', { applied: true })
    }

    async checkMailboxIndicator() {
      return envelope('checkMailboxIndicatorResponse', {
        hasNewMail: harness.unreadMailCount > 0,
        newMailCount: harness.unreadMailCount,
      })
    }

    async openMailbox() {
      return envelope('openMailboxResponse', { mails: [], nextPageToken: '' })
    }
  }

  return { FarmWebSocket }
})

async function signIn() {
  const wrapper = mount(App, { attachTo: document.body })
  const inputs = wrapper.findAll('.login-form input')
  await inputs[0].setValue('tester')
  await inputs[1].setValue('password1234')
  await wrapper.find('.login-form').trigger('submit')
  await flushPromises()
  return wrapper
}

describe('game shell navigation', () => {
  it('fills the unread mail count during the first login', async () => {
    harness.unreadMailCount = 7
    const wrapper = await signIn()
    const mailboxButton = wrapper
      .findAll('.top-nav__link')
      .find((candidate) => candidate.text().startsWith('邮箱'))

    expect(mailboxButton?.find('.mail-count-badge').text()).toBe('7')
    await mailboxButton!.trigger('click')
    await flushPromises()
    expect(mailboxButton?.find('.mail-count-badge').text()).toBe('7')
    wrapper.unmount()
    harness.unreadMailCount = 0
  })

  it('accepts an authoritative zero-count mail push', async () => {
    harness.unreadMailCount = 1
    const wrapper = await signIn()
    const mailboxButton = wrapper
      .findAll('.top-nav__link')
      .find((candidate) => candidate.text().startsWith('邮箱'))
    expect(mailboxButton?.find('.mail-count-badge').text()).toBe('1')

    const socket = harness.sockets.at(-1) as { emitMailCount: (count: number) => void }
    socket.emitMailCount(0)
    await flushPromises()
    expect(mailboxButton?.find('.mail-count-badge').exists()).toBe(false)

    wrapper.unmount()
    harness.unreadMailCount = 0
  })

  it('shows unseen compendium updates until the compendium drawer closes', async () => {
    localStorage.setItem(
      `classic-farm:compendium-seen:${harness.playerId.toString()}`,
      JSON.stringify([]),
    )
    harness.unlockedCropIds = [2001]

    const wrapper = await signIn()
    const compendiumButton = wrapper
      .findAll('.top-nav__link')
      .find((candidate) => candidate.text().startsWith('图鉴'))

    expect(compendiumButton?.find('.red-dot').exists()).toBe(true)
    await compendiumButton!.trigger('click')
    await flushPromises()
    expect(wrapper.find('.compendium-new-dot').exists()).toBe(true)

    await compendiumButton!.trigger('click')
    await flushPromises()
    expect(compendiumButton?.find('.red-dot').exists()).toBe(false)
    expect(
      JSON.parse(
        localStorage.getItem(
          `classic-farm:compendium-seen:${harness.playerId.toString()}`,
        ) ?? '[]',
      ),
    ).toEqual([2001])

    wrapper.unmount()
    harness.unlockedCropIds = []
    localStorage.clear()
  })

  it('shows friend-farm indicators returned by the login friend query', async () => {
    harness.friends = [{ playerId: 84n, accountName: 'test123455', mayHaveStealableCrop: true }]
    const wrapper = await signIn()
    const friendButton = wrapper.findAll('.top-nav__link').find((candidate) => candidate.text().startsWith('好友'))
    expect(friendButton?.find('.red-dot').exists()).toBe(true)
    await friendButton!.trigger('click')
    await flushPromises()
    expect(friendButton?.find('.red-dot').exists()).toBe(false)
    expect(wrapper.find('.friends-list .red-dot').exists()).toBe(true)
    const enterButton = wrapper.find('.friend-enter')
    await enterButton.trigger('click')
    await flushPromises()
    expect(wrapper.find('.friends-list .red-dot').exists()).toBe(true)
    wrapper.unmount()
    harness.friends = []
  })

  it('enters the shell after a successful handshake', async () => {
    const wrapper = await signIn()
    expect(wrapper.find('.game-shell').exists()).toBe(true)
    expect(wrapper.find('.top-nav').exists()).toBe(true)
    wrapper.unmount()
  })

  it('opens a drawer for every navigation entry and closes on re-click', async () => {
    const wrapper = await signIn()
    const labels = ['账号', '商店', '宠物', '好友', '邮箱', '任务', '仓库']

    for (const label of labels) {
      const button = wrapper
        .findAll('.top-nav__link')
        .find((candidate) => candidate.text().startsWith(label))
      expect(button, `导航按钮「${label}」不存在`).toBeTruthy()

      await button!.trigger('click')
      await flushPromises()
      const drawer = wrapper.find('.drawer')
      expect(drawer.exists(), `点击「${label}」后抽屉没有出现`).toBe(true)
      expect(drawer.attributes('aria-label')).toBe(label)

      await button!.trigger('click')
      await flushPromises()
      expect(wrapper.find('.drawer').exists(), `再次点击「${label}」后抽屉没有关闭`).toBe(false)
    }

    wrapper.unmount()
  })

  it('loads the seed catalog and only lists seeds the player owns', async () => {
    const wrapper = await signIn()
    expect(wrapper.text()).not.toContain('种子目录未加载')
    const seedNames = wrapper.findAll('.seed-chip').map((chip) => chip.text())
    expect(seedNames.join(' | ')).toContain('胡萝卜')
    expect(seedNames.join(' | ')).not.toContain('土豆')
    wrapper.unmount()
  })

  it('formats uint64 maturity without mixing BigInt into Number arithmetic', async () => {
    const wrapper = await signIn()
    const tooltips = wrapper
      .findAll('.seed-chip__tip')
      .map((tip) => tip.text())
      .join(' | ')

    expect(tooltips).toContain('胡萝卜的成熟时间是 1 分 40 秒')
    wrapper.unmount()
  })

  it('shows a reconnect notice when the socket drops', async () => {
    const wrapper = await signIn()
    expect(wrapper.find('.shell-notice').exists()).toBe(false)

    const socket = harness.sockets.at(-1) as { disconnect: () => void }
    socket.disconnect()
    await flushPromises()

    expect(wrapper.find('.shell-notice').text()).toContain('实时连接已断开')
    wrapper.unmount()
  })
})
