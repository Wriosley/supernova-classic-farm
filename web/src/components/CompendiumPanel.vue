<script setup lang="ts">
import { computed } from 'vue'
import type {
  CropCatalogEntryView,
  CropCompendiumView,
  PlayerCareerView,
} from '../gen/classicfarm/v1/ws/ws_pb'
import { matureCropSprite } from '../lib/crop-art'

const props = defineProps<{
  career?: PlayerCareerView
  catalog: CropCatalogEntryView[]
  compendium?: CropCompendiumView | null
  ownerLabel?: string
}>()

const unlocked = computed(() => new Set(props.compendium?.unlockedCropIds ?? []))

const entries = computed(() =>
  props.catalog.map((crop) => ({
    crop,
    sprite: matureCropSprite(crop.cropId),
    lit: unlocked.value.has(crop.cropId),
  })),
)

const litCount = computed(() => entries.value.filter((entry) => entry.lit).length)

const compendiumTitle = computed(() =>
  props.ownerLabel ? `${props.ownerLabel} 的图鉴` : '我的图鉴',
)
</script>

<template>
  <div class="compendium-panel">
    <section>
      <h4>用户生涯</h4>
      <p>累计收获作物：{{ career?.totalHarvestedCropQuantity ?? 0n }}</p>
      <p>累计摘取好友作物：{{ career?.totalStolenCropQuantity ?? 0n }}</p>
    </section>

    <section>
      <h4>{{ compendiumTitle }}</h4>
      <p class="compendium-progress">已点亮 {{ litCount }} / {{ entries.length }}</p>
      <ul v-if="entries.length" class="compendium-grid">
        <li
          v-for="entry in entries"
          :key="entry.crop.cropId"
          :class="{ locked: !entry.lit }"
        >
          <img class="pixel-art" :src="entry.sprite" alt="" />
          <strong>{{ entry.crop.name || `作物#${entry.crop.cropId}` }}</strong>
          <small>{{ entry.lit ? '已点亮' : '未解锁' }}</small>
        </li>
      </ul>
      <p v-else class="compendium-progress">作物目录尚未加载。</p>
    </section>
  </div>
</template>

<style scoped>
.compendium-panel {
  display: grid;
  gap: 0.9rem;
}

h4,
p {
  margin: 0;
}

.compendium-progress {
  margin-top: 0.35rem;
  font-size: 0.8rem;
  opacity: 0.75;
}

.compendium-grid {
  list-style: none;
  margin: 0.6rem 0 0;
  padding: 0;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(5.2rem, 1fr));
  gap: 0.5rem;
}

.compendium-grid li {
  display: grid;
  justify-items: center;
  gap: 0.15rem;
  padding: 0.45rem 0.3rem;
  border: 2px solid #cbd5b4;
  border-radius: 0.6rem;
  background: #fbfdf2;
  text-align: center;
}

.compendium-grid img {
  width: 2.2rem;
  height: 2.2rem;
  object-fit: contain;
}

.compendium-grid strong {
  font-size: 0.75rem;
}

.compendium-grid small {
  font-size: 0.65rem;
  opacity: 0.7;
}

.compendium-grid li.locked {
  border-color: #dfe3d6;
  background: #f2f3ec;
  color: #98a2b3;
}

.compendium-grid li.locked img {
  filter: grayscale(1);
  opacity: 0.45;
}
</style>
