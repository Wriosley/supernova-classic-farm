import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ShopPanel from '../components/ShopPanel.vue'

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

function mountShop() {
  return mount(ShopPanel, {
    props: {
      shopEntries: [
        { shopEntryId: 5001, itemId: 1001, unitPrice: 2n, priceVersion: 1n, enabled: true },
        { shopEntryId: 9001, itemId: 1, unitPrice: 3n, priceVersion: 1n, enabled: true },
      ],
      cropCatalog,
      inventory: new Map([[1001, 7]]),
      coinBalance: 500n,
      connected: true,
    } as never,
  })
}

describe('shop seeds', () => {
  it('lists every seed on its own row instead of a name picker', () => {
    const wrapper = mountShop()
    expect(wrapper.find('.crop-picker').exists()).toBe(false)
    const rows = wrapper.findAll('.seed-row')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('胡萝卜种子')
    expect(rows[0].text()).toContain('2 金币 / 粒')
    expect(rows[0].text()).toContain('仓库 7 粒')
    expect(rows[1].text()).toContain('土豆种子')
  })

  it('buys the seed of the row that was expanded', async () => {
    const wrapper = mountShop()
    const row = wrapper.findAll('.seed-row')[1]
    expect(row.find('.seed-detail').exists()).toBe(false)

    await row.find('.seed-summary').trigger('click')
    expect(row.find('.seed-detail').text()).toContain('3725 秒成熟')
    expect(row.text()).toContain('合计 12 金币')

    await row.find('button.primary').trigger('click')
    expect(wrapper.emitted('action')?.[0][0]).toEqual({
      action: 'buy',
      quantity: 3,
      shopEntryId: 5002,
      seedItemId: 1003,
      priceVersion: 1n,
    })
  })

  it('keeps the quantity of each row independent', async () => {
    const wrapper = mountShop()
    const rows = wrapper.findAll('.seed-row')
    await rows[0].find('.seed-summary').trigger('click')
    await rows[1].find('.seed-summary').trigger('click')

    await rows[0].find('[aria-label="增加胡萝卜种子购买数量"]').trigger('click')
    expect(rows[0].text()).toContain('购买 4 粒')
    expect(rows[1].text()).toContain('购买 3 粒')
  })
})
