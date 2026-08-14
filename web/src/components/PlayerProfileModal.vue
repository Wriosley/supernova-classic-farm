<script setup lang="ts">
import type {
  CropCatalogEntryView,
  CropCompendiumView,
  PlayerCareerView,
} from '../gen/classicfarm/v1/ws/ws_pb'
import CompendiumPanel from './CompendiumPanel.vue'

defineProps<{
  open: boolean
  title: string
  career?: PlayerCareerView
  catalog: CropCatalogEntryView[]
  compendium?: CropCompendiumView | null
  ownerLabel?: string
}>()

const emit = defineEmits<{
  close: []
}>()
</script>

<template>
  <div v-if="open" class="profile-modal" role="dialog" aria-modal="true" @click.self="emit('close')">
    <div class="profile-modal__card">
      <header>
        <h3>{{ title }}</h3>
        <button type="button" class="ghost" @click="emit('close')">关闭</button>
      </header>

      <CompendiumPanel
        :career="career"
        :catalog="catalog"
        :compendium="compendium"
        :owner-label="ownerLabel"
      />
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
  width: min(100%, 30rem);
  max-height: min(80vh, 40rem);
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

h3 {
  margin: 0;
}

button.ghost {
  border: none;
  background: transparent;
  text-decoration: underline;
  cursor: pointer;
}
</style>
