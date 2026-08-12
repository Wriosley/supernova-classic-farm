<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import type { CropCatalogEntryView, ShopEntryView } from '../gen/classicfarm/v1/ws/ws_pb'
import type { FarmActionRequest } from '../lib/farm-actions'

type ShopQuote = Pick<
  ShopEntryView,
  'shopEntryId' | 'itemId' | 'unitPrice' | 'priceVersion' | 'enabled'
>

import seedIcon from '../../../frontend/src/assets/art/runtime/items/demo-seed.png'
import fertilizerIcon from '../../../frontend/src/assets/art/runtime/items/fertilizer-basic.png'

const props = defineProps<{
  shopEntries: ShopEntryView[]
  cropCatalog: CropCatalogEntryView[]
  inventory: Map<number, number>
  coinBalance?: bigint
  connected: boolean
  busyAction?: FarmActionRequest
}>()

const emit = defineEmits<{
  action: [request: FarmActionRequest]
}>()

const DEFAULT_SEED_QUANTITY = 3

const seedQuantities = reactive(new Map<number, number>())
const expandedCropIds = reactive(new Set<number>())
const fertilizerBuyQuantity = ref(1)

const seedCrops = computed(() => props.cropCatalog.filter((crop) => crop.seedShopEntryId > 0))
const fertilizerQuote = computed(() => props.shopEntries.find((entry) => entry.itemId === 1))
const fertilizerQuantity = computed(() => props.inventory.get(1) ?? 0)
const fertilizerBuyTotal = computed(
  () => (fertilizerQuote.value?.unitPrice ?? 0n) * BigInt(fertilizerBuyQuantity.value),
)
const canBuyFertilizer = computed(() =>
  Boolean(
    props.connected &&
      fertilizerQuote.value?.enabled &&
      props.coinBalance !== undefined &&
      fertilizerBuyQuantity.value >= 1 &&
      fertilizerBuyQuantity.value <= 50 &&
      fertilizerQuantity.value + fertilizerBuyQuantity.value <= 300 &&
      props.coinBalance >= fertilizerBuyTotal.value,
  ),
)

// The catalog carries the price the crop was published with; the shop response
// is the authoritative quote when it lists the entry.
function quoteFor(crop: CropCatalogEntryView): ShopQuote {
  return (
    props.shopEntries.find((entry) => entry.shopEntryId === crop.seedShopEntryId) ?? {
      shopEntryId: crop.seedShopEntryId,
      itemId: crop.seedItemId,
      unitPrice: crop.seedUnitPrice,
      priceVersion: crop.seedPriceVersion,
      enabled: true,
    }
  )
}

function ownedOf(crop: CropCatalogEntryView): number {
  return props.inventory.get(crop.seedItemId) ?? 0
}

function quantityOf(crop: CropCatalogEntryView): number {
  return seedQuantities.get(crop.cropId) ?? DEFAULT_SEED_QUANTITY
}

function setQuantity(crop: CropCatalogEntryView, quantity: number): void {
  seedQuantities.set(crop.cropId, Math.min(50, Math.max(1, Math.trunc(Number(quantity) || 1))))
}

function totalOf(crop: CropCatalogEntryView): bigint {
  return quoteFor(crop).unitPrice * BigInt(quantityOf(crop))
}

function canBuySeed(crop: CropCatalogEntryView): boolean {
  const quantity = quantityOf(crop)
  return Boolean(
    props.connected &&
      quoteFor(crop).enabled &&
      props.coinBalance !== undefined &&
      quantity >= 1 &&
      quantity <= 50 &&
      ownedOf(crop) + quantity <= 300 &&
      props.coinBalance >= totalOf(crop),
  )
}

function isExpanded(crop: CropCatalogEntryView): boolean {
  return expandedCropIds.has(crop.cropId)
}

function toggleSeed(crop: CropCatalogEntryView): void {
  if (!expandedCropIds.delete(crop.cropId)) {
    expandedCropIds.add(crop.cropId)
  }
}

function clampFertilizerBuy(): void {
  fertilizerBuyQuantity.value = Math.min(
    50,
    Math.max(1, Math.trunc(Number(fertilizerBuyQuantity.value) || 1)),
  )
}

function run(request: FarmActionRequest): void {
  if (!props.busyAction) {
    emit('action', request)
  }
}
</script>

<template>
  <div class="shop-panel-body">
    <div class="panel-heading">
      <h3>种子</h3>
      <span class="price-tag">共 {{ seedCrops.length }} 种</span>
    </div>

    <p v-if="seedCrops.length === 0" class="seed-empty">种子目录尚未加载。</p>

    <ul class="seed-list">
      <li v-for="crop in seedCrops" :key="crop.cropId" class="seed-row">
        <button
          type="button"
          class="seed-summary"
          :aria-expanded="isExpanded(crop)"
          @click="toggleSeed(crop)"
        >
          <img class="item-icon pixel-art" :src="seedIcon" alt="" />
          <span class="shop-copy">
            <strong>{{ crop.name }}种子</strong>
            <small>{{ quoteFor(crop).unitPrice }} 金币 / 粒 · 仓库 {{ ownedOf(crop) }} 粒</small>
          </span>
          <span class="seed-caret" aria-hidden="true">{{ isExpanded(crop) ? '▾' : '▸' }}</span>
        </button>

        <div v-if="isExpanded(crop)" class="seed-detail">
          <small>
            {{ crop.maturitySeconds }} 秒成熟 · 产量 {{ crop.baseYield }} · 仓库上限 300
          </small>
          <div class="quantity-row">
            <button
              type="button"
              :aria-label="`减少${crop.name}种子购买数量`"
              @click="setQuantity(crop, quantityOf(crop) - 1)"
            >
              −
            </button>
            <input
              type="number"
              inputmode="numeric"
              min="1"
              max="50"
              :value="quantityOf(crop)"
              :aria-label="`${crop.name}种子购买数量`"
              @change="setQuantity(crop, Number(($event.target as HTMLInputElement).value))"
            />
            <button
              type="button"
              :aria-label="`增加${crop.name}种子购买数量`"
              @click="setQuantity(crop, quantityOf(crop) + 1)"
            >
              ＋
            </button>
            <span>合计 {{ totalOf(crop) }} 金币</span>
            <button
              class="primary"
              type="button"
              :disabled="!canBuySeed(crop) || Boolean(busyAction)"
              @click="
                run({
                  action: 'buy',
                  quantity: quantityOf(crop),
                  shopEntryId: quoteFor(crop).shopEntryId,
                  seedItemId: crop.seedItemId,
                  priceVersion: quoteFor(crop).priceVersion,
                })
              "
            >
              {{
                busyAction?.action === 'buy' && busyAction.seedItemId === crop.seedItemId
                  ? '购买中…'
                  : `购买 ${quantityOf(crop)} 粒`
              }}
            </button>
          </div>
        </div>
      </li>
    </ul>

    <div class="panel-heading">
      <h3>肥料</h3>
      <span v-if="fertilizerQuote" class="price-tag">
        {{ fertilizerQuote.unitPrice }} 金币 / 袋
      </span>
    </div>
    <div class="shop-item">
      <img class="item-icon pixel-art" :src="fertilizerIcon" alt="基础肥料" />
      <div class="shop-copy">
        <strong>基础肥料</strong>
        <small>当前持有 {{ fertilizerQuantity }} 袋 · 仓库堆叠上限 300</small>
      </div>
    </div>
    <div class="quantity-row">
      <button
        type="button"
        aria-label="减少肥料购买数量"
        @click="fertilizerBuyQuantity--; clampFertilizerBuy()"
      >
        −
      </button>
      <input
        v-model.number="fertilizerBuyQuantity"
        type="number"
        inputmode="numeric"
        min="1"
        max="50"
        aria-label="肥料购买数量"
        @change="clampFertilizerBuy"
      />
      <button
        type="button"
        aria-label="增加肥料购买数量"
        @click="fertilizerBuyQuantity++; clampFertilizerBuy()"
      >
        ＋
      </button>
      <span>合计 {{ fertilizerBuyTotal }} 金币</span>
      <button
        class="primary"
        type="button"
        :disabled="!canBuyFertilizer || Boolean(busyAction)"
        @click="run({ action: 'buy-fertilizer', quantity: fertilizerBuyQuantity })"
      >
        {{ busyAction?.action === 'buy-fertilizer' ? '购买中…' : `购买 ${fertilizerBuyQuantity} 袋` }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.shop-panel-body {
  display: grid;
  gap: 0.4rem;
}

.shop-panel-body h3 {
  margin: 0;
  color: #24361f;
  font-size: 0.95rem;
}

.seed-list {
  display: grid;
  gap: 0.35rem;
  margin: 0;
  padding: 0;
  list-style: none;
}

.seed-row {
  border: 2px solid #cdbb92;
  border-radius: 0.6rem;
  background: #fffdf4;
}

.seed-summary {
  display: flex;
  align-items: center;
  gap: 0.55rem;
  width: 100%;
  padding: 0.4rem 0.55rem;
  border: none;
  background: transparent;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.seed-caret {
  margin-left: auto;
  color: #6b6247;
}

.seed-detail {
  display: grid;
  gap: 0.35rem;
  padding: 0 0.55rem 0.5rem;
}

.seed-empty {
  margin: 0;
  color: #6b6247;
}
</style>
