<script setup lang="ts">
defineProps<{
  open: boolean
  title: string
  kicker?: string
}>()

const emit = defineEmits<{
  close: []
}>()
</script>

<template>
  <aside v-if="open" class="drawer" role="dialog" :aria-label="title">
    <header class="drawer__head">
      <div>
        <span v-if="kicker" class="panel-kicker">{{ kicker }}</span>
        <h2>{{ title }}</h2>
      </div>
      <button type="button" class="drawer__close" aria-label="关闭面板" @click="emit('close')">
        ×
      </button>
    </header>
    <div class="drawer__body">
      <slot />
    </div>
  </aside>
</template>

<style scoped>
/* The drawer deliberately has no backdrop: the farm stays visible and clickable
   while a panel is open, which is what the navigation is meant to allow. */
.drawer {
  position: fixed;
  top: 4.6rem;
  right: max(1rem, calc(50vw - 37rem));
  bottom: 1rem;
  z-index: 30;
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  width: min(26rem, calc(100vw - 2rem));
  border: 2px solid #8b6c42;
  border-radius: 1rem;
  background: #fff8dc;
  box-shadow: 0 1.2rem 2.6rem rgb(42 61 35 / 28%);
}

.drawer__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 0.85rem 1rem;
  border-bottom: 1px solid #d9c79a;
}

.drawer__head h2 {
  margin: 0;
  color: #24361f;
  font-size: 1.05rem;
}

.drawer__close {
  min-width: 2.2rem;
  min-height: 2.2rem;
  font-size: 1.1rem;
  line-height: 1;
}

.drawer__body {
  display: grid;
  align-content: start;
  gap: 0.9rem;
  overflow: auto;
  padding: 1rem;
}

@media (width <= 60rem) {
  .drawer {
    top: auto;
    right: 0.5rem;
    bottom: 0.5rem;
    left: 0.5rem;
    width: auto;
    max-height: 70vh;
  }
}
</style>
