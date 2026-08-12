<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { CropCatalogEntryView, ShopEntryView } from '../gen/classicfarm/v1/ws/ws_pb'
import type { FarmActionRequest } from '../lib/farm-actions'

import seedIcon from '../../../frontend/src/assets/art/runtime/items/demo-seed.png'
import cropIcon from '../../../frontend/src/assets/art/runtime/items/demo-crop.png'
import fertilizerIcon from '../../../frontend/src/assets/art/runtime/items/fertilizer-basic.png'

const props = defineProps<{
  shopEntries: ShopEntryView[]
  cropCatalog: CropCatalogEntryView[]
  inventory: Map<number, number>
  connected: boolean
  busyAction?: FarmActionRequest
}>()

const emit = defineEmits<{
  action: [request: FarmActionRequest]
}>()

const selectedSellCropItemId = ref(0)
const sellQuantity = ref(1)

const fertilizerQuantity = computed(() => props.inventory.get(1) ?? 0)
const seedRows = computed(() =>
  props.cropCatalog
    .filter((crop) => crop.seedShopEntryId > 0)
    .map((crop) => ({
      key: `seed-${crop.seedItemId}`,
      icon: seedIcon,
      name: `${crop.name}种子`,
      quantity: props.inventory.get(crop.seedItemId) ?? 0,
    })),
)
const cropRows = computed(() =>
  props.cropCatalog.map((crop) => ({
    key: `crop-${crop.cropItemId}`,
    icon: cropIcon,
    name: crop.name,
    quantity: props.inventory.get(crop.cropItemId) ?? 0,
  })),
)
const rows = computed(() =>
  [
    ...seedRows.value,
    ...cropRows.value,
    { key: 'fertilizer', icon: fertilizerIcon, name: '基础肥料', quantity: fertilizerQuantity.value },
  ].filter((row) => row.quantity > 0),
)
const sellableCrops = computed(() =>
  props.cropCatalog.filter((crop) => (props.inventory.get(crop.cropItemId) ?? 0) > 0),
)
const selectedSellCrop = computed(
  () =>
    sellableCrops.value.find((crop) => crop.cropItemId === selectedSellCropItemId.value) ??
    sellableCrops.value[0],
)
const cropQuote = computed(() => {
  const crop = selectedSellCrop.value
  if (!crop) {
    return undefined
  }
  return (
    props.shopEntries.find(
      (entry) => entry.itemId === crop.cropItemId && entry.unitPrice === crop.sellUnitPrice,
    ) ?? {
      shopEntryId: 0,
      itemId: crop.cropItemId,
      unitPrice: crop.sellUnitPrice,
      priceVersion: crop.sellPriceVersion,
      enabled: true,
    }
  )
})
const cropQuantity = computed(() => {
  const crop = selectedSellCrop.value
  return crop ? props.inventory.get(crop.cropItemId) ?? 0 : 0
})
const sellTotal = computed(() => (cropQuote.value?.unitPrice ?? 0n) * BigInt(sellQuantity.value))
const canSell = computed(() =>
  Boolean(
    props.connected &&
      selectedSellCrop.value &&
      cropQuote.value &&
      sellQuantity.value >= 1 &&
      sellQuantity.value <= cropQuantity.value,
  ),
)

watch(
  sellableCrops,
  (crops) => {
    if (crops.length === 0) {
      selectedSellCropItemId.value = 0
      return
    }
    if (!crops.some((crop) => crop.cropItemId === selectedSellCropItemId.value)) {
      selectedSellCropItemId.value = crops[0].cropItemId
    }
  },
  { immediate: true },
)

watch(cropQuantity, (quantity) => {
  sellQuantity.value = quantity > 0 ? Math.min(Math.max(sellQuantity.value, 1), quantity) : 1
})

function clampSell(): void {
  sellQuantity.value = Math.min(
    Math.max(cropQuantity.value, 1),
    Math.max(1, Math.trunc(Number(sellQuantity.value) || 1)),
  )
}

function run(request: FarmActionRequest): void {
  if (!props.busyAction) {
    emit('action', request)
  }
}
</script>

<template>
  <div class="inventory-panel-body inventory-panel">
    <ul v-if="rows.length" class="inventory-rows">
      <li v-for="row in rows" :key="row.key" class="inventory-slot">
        <img class="pixel-art" :src="row.icon" alt="" />
        <span>{{ row.name }}</span>
        <strong>× {{ row.quantity }}</strong>
      </li>
    </ul>
    <p v-else class="empty-state">仓库还是空的，先去商店买些种子吧。</p>

    <div class="sell-controls">
      <label class="sell-crop-picker">
        出售作物
        <select
          v-model.number="selectedSellCropItemId"
          :disabled="sellableCrops.length === 0"
          aria-label="选择出售作物"
        >
          <option v-if="sellableCrops.length === 0" :value="0">暂无可售作物</option>
          <option v-for="crop in sellableCrops" :key="crop.cropItemId" :value="crop.cropItemId">
            {{ crop.name }} ×{{ inventory.get(crop.cropItemId) ?? 0 }}
          </option>
        </select>
      </label>
      <div class="quantity-row">
        <button type="button" aria-label="减少出售数量" @click="sellQuantity--; clampSell()">
          −
        </button>
        <input
          v-model.number="sellQuantity"
          type="number"
          inputmode="numeric"
          min="1"
          :max="Math.max(cropQuantity, 1)"
          aria-label="出售数量"
          @change="clampSell"
        />
        <button type="button" aria-label="增加出售数量" @click="sellQuantity++; clampSell()">
          ＋
        </button>
        <span>合计 {{ sellTotal }} 金币</span>
      </div>
      <div class="sell-buttons">
        <button
          class="primary"
          type="button"
          :disabled="!canSell || Boolean(busyAction)"
          @click="
            run({
              action: 'sell',
              quantity: sellQuantity,
              cropItemId: selectedSellCrop?.cropItemId,
              priceVersion: cropQuote?.priceVersion,
            })
          "
        >
          {{ busyAction?.action === 'sell' && !busyAction.sellAll ? '出售中…' : `出售 ${sellQuantity}` }}
        </button>
        <button
          type="button"
          :disabled="!canSell || Boolean(busyAction)"
          @click="
            run({
              action: 'sell',
              sellAll: true,
              cropItemId: selectedSellCrop?.cropItemId,
              priceVersion: cropQuote?.priceVersion,
            })
          "
        >
          {{ busyAction?.action === 'sell' && busyAction.sellAll ? '全部出售中…' : '全部出售' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.inventory-panel-body {
  display: grid;
  gap: 0.6rem;
}

.inventory-rows {
  display: grid;
  gap: 0.4rem;
  margin: 0;
  padding: 0;
  list-style: none;
}
</style>
