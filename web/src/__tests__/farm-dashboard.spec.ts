import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import FarmDashboard from '../components/FarmDashboard.vue'
import { PlotState } from '../gen/classicfarm/v1/ws/plot/plot_state_pb'

const now = BigInt(Date.now())

const stateCycle = [PlotState.EMPTY, PlotState.GROWING, PlotState.MATURE, PlotState.NEED_CLEANUP]
// Plot 3 grew a watermelon, plot 7 a crop the client has no sprite for.
const cropIdByPlotId: Record<number, number> = { 3: 2010, 7: 9999 }

const plots = Array.from({ length: 16 }, (_, index) => {
  const plotId = index + 1
  const plotState = stateCycle[index % stateCycle.length]
  return {
    plotId,
    plotState,
    cropId: plotState === PlotState.EMPTY ? 0 : (cropIdByPlotId[plotId] ?? 2001),
    harvestableQuantity: plotState === PlotState.MATURE ? 3 : 0,
    estimatedMatureAtMs: plotState === PlotState.GROWING ? now + 90_000n : 0n,
  }
})

const cropCatalog = [
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

function mountFarm(
  inventory: Array<{ itemId: number; quantity: number }> = [
    { itemId: 1, quantity: 2 },
    { itemId: 1003, quantity: 4 },
  ],
  activePet?: { petId: number; name: string; foodActiveUntilMs: bigint },
) {
  return mount(FarmDashboard, {
    props: {
      snapshot: {
        playerId: 7n,
        coinBalance: 29n,
        inventory,
        plots,
        currentChapter: { chapterId: 1, status: 1, tasks: [] },
      },
      cropCatalog,
      connected: true,
      nowMs: now,
      activePet,
    } as never,
  })
}

describe('farm plots', () => {
  it('renders every plot the snapshot carries', () => {
    const wrapper = mountFarm()
    expect(wrapper.findAll('.plot-tile')).toHaveLength(16)
  })

  // The plot label and countdown are painted straight onto the grass, so they
  // must live inside the caption layer that stacks above the soil sprite.
  it('keeps the plot text inside the overlay caption', () => {
    const wrapper = mountFarm()
    const caption = wrapper.find('.plot-tile .plot-caption')
    expect(caption.find('strong').text()).toBe('空地')
    expect(caption.find('small').text()).toContain('空地可种植')
  })

  it('shows the sprite of the crop that actually matured', () => {
    const wrapper = mountFarm()
    const cropOf = (plotId: number) =>
      wrapper.findAll('.plot-tile')[plotId - 1].find('.plot-crop').attributes('src') ?? ''

    expect(cropOf(3)).toContain('watermelon-mature')
    // An unmapped crop id must still render something instead of a blank plot.
    expect(cropOf(7)).toContain('demo-mature')
  })

  it('keeps a harvest tip visible on every mature plot', () => {
    const wrapper = mountFarm()
    const maturePlots = wrapper
      .findAll('.plot-tile')
      .filter((_, index) => plots[index].plotState === PlotState.MATURE)

    expect(maturePlots.length).toBeGreaterThan(0)
    expect(maturePlots.every((plot) => plot.find('.plot-float.persistent').text() === '可以收获')).toBe(
      true,
    )
  })

  it('emits plot feedback instead of a banner when the selected tool is invalid', async () => {
    const wrapper = mountFarm()
    await wrapper.findAll('.plot-tile')[0].trigger('click')

    expect(wrapper.find('.action-notice').exists()).toBe(false)
    expect(wrapper.emitted('plotFeedback')).toEqual([[1, '还不能收获。']])
  })

  it('renders the current visitor bar', () => {
    const wrapper = mount(FarmDashboard, {
      props: {
        snapshot: {
          playerId: 7n,
          coinBalance: 29n,
          inventory: [],
          plots,
          currentChapter: { chapterId: 1, status: 1, tasks: [] },
        },
        cropCatalog,
        connected: true,
        nowMs: now,
        visitors: ['alice'],
      } as never,
    })

    expect(wrapper.find('.farm-visitors').text()).toContain('alice 进入农场')
  })
})

describe('seed bar', () => {
  it('lists only owned seeds and keeps the maturity tooltip', () => {
    const wrapper = mountFarm()
    const chips = wrapper.findAll('.seed-chip')
    expect(chips).toHaveLength(1)
    expect(chips[0].text()).toContain('土豆')
    expect(chips[0].text()).toContain('×4')
    expect(chips[0].find('.seed-chip__tip').text()).toBe('土豆的成熟时间是 62 分 5 秒')
  })

  it('says the warehouse is empty instead of showing empty seed baskets', () => {
    const wrapper = mountFarm([{ itemId: 1, quantity: 2 }])
    expect(wrapper.findAll('.seed-chip')).toHaveLength(0)
    expect(wrapper.find('.farm-bar__empty').text()).toBe('仓库里还没有种子')
    expect(wrapper.text()).not.toContain('种子目录未加载')
  })
})

describe('deployed pet', () => {
  const inventory = [{ itemId: 1, quantity: 2 }]

  it('keeps an empty pet slot when nobody is on duty', () => {
    const pet = mountFarm(inventory).find('.farm-pet')
    expect(pet.exists()).toBe(true)
    expect(pet.classes()).toContain('empty')
    expect(pet.find('.farm-pet__status').text()).toBe('尚未获得宠物')
  })

  it('guards next to the farm with the breed and the remaining time', () => {
    const wrapper = mountFarm(inventory, {
      petId: 2,
      name: '牧羊犬',
      foodActiveUntilMs: now + 3_725_000n,
    })
    const pet = wrapper.find('.farm-pet')
    expect(pet.classes()).not.toContain('hungry')
    expect(pet.find('.farm-pet__breed').text()).toBe('牧羊犬')
    expect(pet.find('.farm-pet__status').text()).toBe('牧羊犬护卫中（时间：01:02:05）')
    expect(pet.find('.farm-pet__art').attributes('src')).toContain('shepherd-dog.png')
  })

  it('turns sad and says it is hungry once the food ran out', () => {
    const wrapper = mountFarm(inventory, {
      petId: 1,
      name: '田园犬',
      foodActiveUntilMs: now - 1_000n,
    })
    const pet = wrapper.find('.farm-pet')
    expect(pet.classes()).toContain('hungry')
    expect(pet.find('.farm-pet__status').text()).toBe('田园犬现在很饿')
    expect(pet.find('.farm-pet__art').attributes('src')).toContain('village-dog-sad.png')
  })
})
