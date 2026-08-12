<script setup lang="ts">
import { panelOrder, panelTitles, type PanelId } from '../lib/panels'
import coinIcon from '../../../frontend/src/assets/art/runtime/items/coin.png'

const props = defineProps<{
  accountName: string
  coinBalance?: bigint
  activePanel: PanelId | null
  mailRedDot: boolean
  friendRedDot: boolean
}>()

const emit = defineEmits<{
  select: [panel: PanelId]
  openProfile: []
}>()

function redDot(panel: PanelId): boolean {
  switch (panel) {
    case 'mailbox':
      return props.mailRedDot
    case 'friends':
      return props.friendRedDot
    default:
      return false
  }
}
</script>

<template>
  <header class="top-nav">
    <div class="top-nav__identity">
      <button type="button" class="top-nav__name" @click="emit('openProfile')">
        {{ accountName || '玩家' }}
      </button>
      <span class="top-nav__wallet">
        <img class="pixel-art" :src="coinIcon" alt="" />
        <strong>{{ coinBalance?.toString() ?? '—' }}</strong>
      </span>
    </div>

    <nav class="top-nav__links" aria-label="主导航">
      <button
        v-for="panel in panelOrder"
        :key="panel"
        type="button"
        class="top-nav__link"
        :class="{ selected: activePanel === panel }"
        :aria-pressed="activePanel === panel"
        @click="emit('select', panel)"
      >
        {{ panelTitles[panel] }}
        <span v-if="redDot(panel)" class="red-dot" aria-label="有新消息" />
      </button>
    </nav>
  </header>
</template>

<style scoped>
.top-nav {
  position: sticky;
  top: 0;
  z-index: 20;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.6rem 1rem;
  padding: 0.55rem 0.9rem;
  border: 2px solid #71895e;
  border-radius: 0.9rem;
  background: #fffdf2;
  box-shadow: 0 0.5rem 1.4rem rgb(42 61 35 / 14%);
}

.top-nav__identity {
  display: flex;
  align-items: center;
  gap: 0.8rem;
}

.top-nav__name {
  padding: 0;
  border: none;
  background: transparent;
  color: #24361f;
  font-size: 1rem;
  font-weight: 800;
  text-decoration: underline;
  text-underline-offset: 0.2em;
}

.top-nav__wallet {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  padding: 0.25rem 0.6rem;
  border: 2px solid #76542a;
  border-radius: 0.7rem;
  background: #fff4bd;
}

.top-nav__wallet img {
  width: 1.2rem;
  height: 1.2rem;
}

.top-nav__links {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
}

.top-nav__link {
  position: relative;
  min-height: 2.4rem;
  padding: 0.35rem 0.75rem;
  font-size: 0.85rem;
  font-weight: 750;
}

.top-nav__link.selected {
  border-color: #31552d;
  background: #dfecc2;
  box-shadow: 0 0 0 3px rgb(49 85 45 / 15%);
}

.top-nav__link .red-dot {
  position: absolute;
  top: 0.25rem;
  right: 0.25rem;
}
</style>
