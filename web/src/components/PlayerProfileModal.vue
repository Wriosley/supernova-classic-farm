<script setup lang="ts">
import { computed } from 'vue'
import type {
  CropCatalogEntryView,
  CropCompendiumView,
  PlayerCareerView,
} from '../gen/classicfarm/v1/ws/ws_pb'

const props = defineProps<{
  open: boolean
  title: string
  career?: PlayerCareerView
  catalog: CropCatalogEntryView[]
  compendium?: CropCompendiumView | null
  showCompendium: boolean
}>()

const emit = defineEmits<{
  close: []
}>()

const unlocked = computed(() => new Set(props.compendium?.unlockedCropIds ?? []))

const entries = computed(() =>
  props.catalog.map((crop) => ({
    crop,
    lit: unlocked.value.has(crop.cropId),
  })),
)
</script>

<template>
  <div v-if="open" class="profile-modal" role="dialog" aria-modal="true" @click.self="emit('close')">
    <div class="profile-modal__card">
      <header>
        <h3>{{ title }}</h3>
        <button type="button" class="ghost" @click="emit('close')">关闭</button>
      </header>

      <section>
        <h4>用户生涯</h4>
        <p>累计收获作物：{{ career?.totalHarvestedCropQuantity ?? 0n }}</p>
        <p>累计摘取好友作物：{{ career?.totalStolenCropQuantity ?? 0n }}</p>
      </section>

      <section v-if="showCompendium">
        <h4>我的图鉴</h4>
        <ul class="compendium-list">
          <li
            v-for="entry in entries"
            :key="entry.crop.cropId"
            :class="{ locked: !entry.lit }"
          >
            {{ entry.crop.name || `作物#${entry.crop.cropId}` }}
            <span>{{ entry.lit ? '已点亮' : '未解锁' }}</span>
          </li>
        </ul>
      </section>
    </div>
  </div>
</template>

<style scoped>
.profile-modal {
  position: fixed;
  inset: 0;
  z-index: 40;
  display: grid;
  place-items: center;
  padding: 1rem;
  background: color-mix(in srgb, #101828 45%, transparent);
}

.profile-modal__card {
  width: min(100%, 26rem);
  max-height: min(80vh, 36rem);
  overflow: auto;
  padding: 1rem;
  border-radius: 0.75rem;
  background: #fffaf3;
  color: #1f2937;
  display: grid;
  gap: 0.9rem;
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

.compendium-list {
  list-style: none;
  margin: 0.5rem 0 0;
  padding: 0;
  display: grid;
  gap: 0.4rem;
}

.compendium-list li {
  display: flex;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 0.35rem 0;
  border-bottom: 1px solid color-mix(in srgb, currentColor 10%, transparent);
}

.compendium-list li.locked {
  color: #98a2b3;
}

button.ghost {
  border: none;
  background: transparent;
  text-decoration: underline;
  cursor: pointer;
}
</style>
