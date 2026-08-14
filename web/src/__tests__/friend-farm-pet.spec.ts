import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import FriendFarmDashboard from '../components/FriendFarmDashboard.vue'
import { PlotState } from '../gen/classicfarm/v1/ws/plot/plot_state_pb'

const now = BigInt(Date.now())

function mountVisit(pet?: {
  activePetId: number
  petName: string
  foodActiveUntilMs: bigint
}) {
  return mount(FriendFarmDashboard, {
    props: {
      snapshot: {
        ownerPlayerId: 9n,
        plots: [
          {
            plotId: 1,
            plotState: PlotState.EMPTY,
            cropId: 0,
            harvestableQuantity: 0,
            canSteal: false,
          },
        ],
        pet,
      },
      ownerLabel: 'friend',
      cropCatalog: [],
      connected: true,
      busy: false,
      nowMs: now,
    } as never,
  })
}

describe('friend farm pet badge', () => {
  it('shows the empty pet slot when the owner has no dog on duty', () => {
    const pet = mountVisit().find('.farm-pet')
    expect(pet.exists()).toBe(true)
    expect(pet.classes()).toContain('empty')
    expect(pet.find('.farm-pet__status').text()).toBe('尚未获得宠物')
  })

  it('shows the owner dog status next to the lawn', () => {
    const wrapper = mountVisit({
      activePetId: 1,
      petName: '田园犬',
      foodActiveUntilMs: now + 65_000n,
    })
    const pet = wrapper.find('.farm-pet')
    expect(pet.classes()).not.toContain('empty')
    expect(pet.find('.farm-pet__breed').text()).toBe('田园犬')
    expect(pet.find('.farm-pet__status').text()).toBe('田园犬护卫中（时间：00:01:05）')
    expect(pet.find('.farm-pet__art').attributes('src')).toContain('village-dog.png')
  })
})

describe('friend farm plot feedback', () => {
  it('emits a plot float message when the selected action cannot target the plot', async () => {
    const wrapper = mountVisit()
    await wrapper.find('.plot-tile').trigger('click')

    expect(wrapper.find('.action-notice').exists()).toBe(false)
    expect(wrapper.emitted('plotFeedback')).toEqual([[1, '这块地现在不能偷。']])
  })
})
