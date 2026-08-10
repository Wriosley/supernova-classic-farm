<script setup lang="ts">
import { computed } from 'vue'
import type { PetPanelView, PetShopEntryView } from '../gen/classicfarm/v1/ws/ws_pb'

const props = defineProps<{
  panel: PetPanelView | null
  nowMs: bigint
  busyBuyPetId: number | null
  busyDeployPetId: number | null
  busyBuyFood: boolean
  busyFeed: boolean
  error: string
  message: string
}>()

const emit = defineEmits<{
  buyPet: [pet: PetShopEntryView]
  deployPet: [petId: number]
  buyFood: []
  feed: []
  refresh: []
}>()

const activeName = computed(() => {
  const activeId = props.panel?.activePetId ?? 0
  if (!activeId) {
    return '无'
  }
  return props.panel?.pets.find((pet) => pet.petId === activeId)?.name ?? `宠物#${activeId}`
})

const foodRemainingLabel = computed(() => {
  const until = props.panel?.foodActiveUntilMs ?? 0n
  if (until <= props.nowMs) {
    return '00:00:00'
  }
  const totalSeconds = Number((until - props.nowMs) / 1000n)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  return `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`
})

const satietyLabel = computed(() => {
  const until = props.panel?.foodActiveUntilMs ?? 0n
  return until > props.nowMs ? '充足' : '饥饿'
})

function formatBps(bps: number): string {
  return `${(bps / 100).toFixed(bps % 100 === 0 ? 0 : 2)}%`
}
</script>

<template>
  <div class="pet-panel">
    <div class="pet-panel__header">
      <h3 id="pet-title">宠物商店</h3>
      <button type="button" class="ghost" :disabled="busyBuyFood || busyFeed" @click="emit('refresh')">
        刷新
      </button>
    </div>

    <p v-if="error" class="pet-panel__error">{{ error }}</p>
    <p v-else-if="message" class="pet-panel__message">{{ message }}</p>

    <ul v-if="panel" class="pet-list">
      <li v-for="pet in panel.pets" :key="pet.petId" class="pet-item">
        <div class="pet-item__body">
          <strong>{{ pet.name }}</strong>
          <p>价格：{{ pet.priceCoins }}金币</p>
          <p>护主概率：{{ formatBps(pet.guardProbabilityBps) }}</p>
          <p>触发罚款：{{ pet.guardPenaltyCoins }}金币</p>
        </div>
        <div class="pet-item__actions">
          <button
            v-if="!pet.owned"
            type="button"
            :disabled="busyBuyPetId !== null"
            @click="emit('buyPet', pet)"
          >
            {{ busyBuyPetId === pet.petId ? '购买中…' : '购买' }}
          </button>
          <button
            v-else
            type="button"
            :disabled="busyDeployPetId !== null || panel.activePetId === pet.petId"
            @click="emit('deployPet', pet.petId)"
          >
            {{
              panel.activePetId === pet.petId
                ? '已出战'
                : busyDeployPetId === pet.petId
                  ? '派出中…'
                  : '派出'
            }}
          </button>
        </div>
      </li>
    </ul>

    <div v-if="panel" class="pet-status">
      <p>当前出战：{{ activeName }}</p>
      <p>狗粮数量：{{ panel.petFoodQuantity }}</p>
      <p>饱食状态：{{ satietyLabel }}</p>
      <p>剩余时间：{{ foodRemainingLabel }}</p>
      <p>护主状态：{{ panel.guardBuffActive ? '生效' : '未生效' }}</p>
    </div>

    <div v-if="panel?.petFood" class="pet-food">
      <strong>狗粮</strong>
      <p>价格：{{ panel.petFood.unitPrice }}金币</p>
      <p>一份增加：{{ Number(panel.petFood.durationSeconds) / 3600 }}小时</p>
      <div class="pet-food__actions">
        <button type="button" :disabled="busyBuyFood" @click="emit('buyFood')">
          {{ busyBuyFood ? '购买中…' : '购买狗粮' }}
        </button>
        <button type="button" :disabled="busyFeed" @click="emit('feed')">
          {{ busyFeed ? '喂食中…' : '喂食' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.pet-panel {
  display: grid;
  gap: 0.85rem;
  min-width: 0;
}

.pet-panel__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}

.pet-panel__header h3 {
  margin: 0;
  font-size: 1.05rem;
}

.pet-panel__error {
  margin: 0;
  color: #b42318;
}

.pet-panel__message {
  margin: 0;
  color: #027a48;
}

.pet-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 0.75rem;
}

.pet-item,
.pet-status,
.pet-food {
  min-width: 0;
  padding: 0.75rem 0;
  border-top: 1px solid color-mix(in srgb, currentColor 12%, transparent);
}

.pet-item {
  display: grid;
  gap: 0.65rem;
}

.pet-item__body p,
.pet-status p,
.pet-food p {
  margin: 0.2rem 0 0;
  font-size: 0.92rem;
}

.pet-item__actions,
.pet-food__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}

button {
  min-height: 2.25rem;
  padding: 0.35rem 0.8rem;
  border: 1px solid color-mix(in srgb, currentColor 25%, transparent);
  background: transparent;
  color: inherit;
  cursor: pointer;
}

button:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

button.ghost {
  border: none;
  text-decoration: underline;
}

@media (min-width: 480px) {
  .pet-item {
    grid-template-columns: 1fr auto;
    align-items: end;
  }
}
</style>
