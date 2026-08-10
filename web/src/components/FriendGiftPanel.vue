<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { CropCatalogEntryView } from '../gen/classicfarm/v1/ws/ws_pb'

const props = defineProps<{
  open: boolean
  recipientName: string
  recipientPlayerId: bigint
  crops: CropCatalogEntryView[]
  inventory: Map<number, number>
  busy: boolean
  error: string
  message: string
}>()

const emit = defineEmits<{
  close: []
  send: [cropItemId: number, quantity: number]
}>()

const selectedCropItemId = ref(0)
const quantity = ref(1)

const availableCrops = computed(() =>
  props.crops.filter((crop) => (props.inventory.get(crop.cropItemId) ?? 0) > 0),
)

const selectedCrop = computed(
  () => availableCrops.value.find((crop) => crop.cropItemId === selectedCropItemId.value) ?? null,
)

const maxQuantity = computed(() => {
  if (!selectedCrop.value) {
    return 0
  }
  return Math.min(10, props.inventory.get(selectedCrop.value.cropItemId) ?? 0)
})

const preview = computed(() => {
  const cropName = selectedCrop.value?.name || '作物'
  const qty = quantity.value
  return {
    title: '好友赠礼',
    content: `将送给对方「${cropName} ×${qty}」。对方邮箱固定模板：标题「好友赠礼」，正文「{你的昵称} 送给你一批作物，记得查收。」`,
  }
})

watch(
  () => [props.open, availableCrops.value] as const,
  ([open, crops]) => {
    if (!open) {
      return
    }
    if (!crops.some((crop) => crop.cropItemId === selectedCropItemId.value)) {
      selectedCropItemId.value = crops[0]?.cropItemId ?? 0
    }
    quantity.value = Math.min(Math.max(1, quantity.value), maxQuantity.value || 1)
  },
  { immediate: true },
)

watch(maxQuantity, (max) => {
  if (quantity.value > max && max > 0) {
    quantity.value = max
  }
})

function submit(): void {
  if (!selectedCrop.value || quantity.value < 1 || quantity.value > maxQuantity.value) {
    return
  }
  emit('send', selectedCrop.value.cropItemId, quantity.value)
}
</script>

<template>
  <div v-if="open" class="gift-modal" role="dialog" aria-modal="true" @click.self="emit('close')">
    <div class="gift-modal__card">
      <header>
        <h3>赠送礼物</h3>
        <button type="button" class="ghost" @click="emit('close')">关闭</button>
      </header>

      <p>收件人：{{ recipientName || `#${recipientPlayerId}` }}</p>
      <p v-if="error" class="gift-error">{{ error }}</p>
      <p v-else-if="message" class="gift-message">{{ message }}</p>

      <label>
        作物
        <select v-model.number="selectedCropItemId" :disabled="busy || !availableCrops.length">
          <option v-if="!availableCrops.length" :value="0">暂无作物库存</option>
          <option
            v-for="crop in availableCrops"
            :key="crop.cropItemId"
            :value="crop.cropItemId"
          >
            {{ crop.name }} ×{{ inventory.get(crop.cropItemId) ?? 0 }}
          </option>
        </select>
      </label>

      <label>
        数量（1–{{ maxQuantity || 10 }}）
        <input
          v-model.number="quantity"
          type="number"
          min="1"
          :max="maxQuantity || 10"
          :disabled="busy || maxQuantity < 1"
        />
      </label>

      <section class="gift-preview">
        <h4>邮件预览</h4>
        <p><strong>{{ preview.title }}</strong></p>
        <p>{{ preview.content }}</p>
      </section>

      <button
        type="button"
        :disabled="busy || !selectedCrop || quantity < 1 || quantity > maxQuantity"
        @click="submit"
      >
        {{ busy ? '发送中…' : '发送礼物' }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.gift-modal {
  position: fixed;
  inset: 0;
  z-index: 40;
  display: grid;
  place-items: center;
  padding: 0.75rem;
  background: color-mix(in srgb, #101828 45%, transparent);
}

.gift-modal__card {
  width: min(100%, 24rem);
  max-height: min(88vh, 36rem);
  overflow: auto;
  padding: 1rem;
  border-radius: 0.75rem;
  background: #fffaf3;
  color: #1f2937;
  display: grid;
  gap: 0.75rem;
}

header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 0.75rem;
}

h3,
h4,
p {
  margin: 0;
}

label {
  display: grid;
  gap: 0.35rem;
}

select,
input {
  min-height: 2.75rem;
  font: inherit;
}

.gift-preview {
  display: grid;
  gap: 0.35rem;
  padding: 0.6rem;
  background: color-mix(in srgb, #d0d5dd 18%, transparent);
  border-radius: 0.5rem;
}

.gift-error {
  color: #b42318;
}

.gift-message {
  color: #027a48;
}

button {
  min-height: 2.75rem;
}

button.ghost {
  border: none;
  background: transparent;
  text-decoration: underline;
  cursor: pointer;
}
</style>
