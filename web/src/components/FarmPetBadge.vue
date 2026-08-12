<script setup lang="ts">
import { computed } from 'vue'
import { petSprite, type DeployedPet } from '../lib/pet-art'

const props = defineProps<{
  pet?: DeployedPet
  nowMs: bigint
  emptyLabel?: string
  interactive?: boolean
}>()

const emit = defineEmits<{
  click: []
}>()

const hungry = computed(() => (props.pet?.foodActiveUntilMs ?? 0n) <= props.nowMs)

const guardCountdown = computed(() => {
  const until = props.pet?.foodActiveUntilMs ?? 0n
  if (until <= props.nowMs) {
    return '00:00:00'
  }
  const total = Number((until - props.nowMs) / 1000n)
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = total % 60
  return [hours, minutes, seconds].map((part) => String(part).padStart(2, '0')).join(':')
})

const statusText = computed(() => {
  const pet = props.pet
  if (!pet) {
    return props.emptyLabel ?? '尚未获得宠物'
  }
  return hungry.value
    ? `${pet.name}现在很饿`
    : `${pet.name}护卫中（时间：${guardCountdown.value}）`
})

function onActivate(): void {
  if (props.interactive) {
    emit('click')
  }
}
</script>

<template>
  <component
    :is="interactive ? 'button' : 'div'"
    type="button"
    class="farm-pet"
    :class="{ hungry: Boolean(pet) && hungry, empty: !pet }"
    :aria-label="statusText"
    @click="onActivate"
  >
    <span class="farm-pet__breed">{{ pet?.name ?? '宠物' }}</span>
    <img
      v-if="pet"
      class="farm-pet__art pixel-art"
      :src="petSprite(pet.petId, hungry)"
      alt=""
    />
    <span v-else class="farm-pet__art farm-pet__art--empty" aria-hidden="true" />
    <span class="farm-pet__status">{{ statusText }}</span>
  </component>
</template>

<style scoped>
.farm-pet {
  display: grid;
  flex: none;
  justify-items: center;
  gap: 0.2rem;
  width: 7.5rem;
  padding: 0.4rem 0.3rem;
  border: 2px solid #8b6c42;
  border-radius: 0.9rem;
  background: #fff8dc;
  color: inherit;
  font: inherit;
  text-align: center;
}

button.farm-pet {
  cursor: pointer;
}

.farm-pet.hungry {
  border-color: #b45f50;
  background: #fdeee6;
}

.farm-pet.empty {
  border-style: dashed;
  border-color: #a89978;
  background: color-mix(in srgb, #fff8dc 70%, #cfc4a4);
  opacity: 0.92;
}

.farm-pet__breed {
  color: #4a5c3c;
  font-size: 0.7rem;
  font-weight: 800;
  letter-spacing: 0.05em;
}

.farm-pet__art {
  width: 3.6rem;
  height: 3.6rem;
}

.farm-pet__art--empty {
  border-radius: 0.55rem;
  background:
    linear-gradient(135deg, transparent 46%, #9a8b6c 46%, #9a8b6c 54%, transparent 54%),
    color-mix(in srgb, #d9ceb0 80%, #fff);
}

.farm-pet__status {
  color: #4d5a44;
  font-size: 0.66rem;
  font-weight: 700;
  line-height: 1.25;
}

.farm-pet.hungry .farm-pet__status,
.farm-pet.empty .farm-pet__status {
  color: #a4482f;
}

.farm-pet.empty .farm-pet__status {
  color: #6b6247;
}
</style>
