<script setup lang="ts">
import { computed } from 'vue'
import type { ChapterView } from '../gen/classicfarm/v1/ws/ws_pb'
import { ChapterStatus } from '../gen/classicfarm/v1/ws/chapter/chapter_status_pb'
import type { FarmActionRequest } from '../lib/farm-actions'

import checkIcon from '../../../frontend/src/assets/art/runtime/ui/check.png'

const props = defineProps<{
  chapter?: ChapterView
  connected: boolean
  busyAction?: FarmActionRequest
}>()

const emit = defineEmits<{
  action: [request: FarmActionRequest]
}>()

const taskNames = new Map<number, string>([
  [1, '购买 3 粒种子'],
  [2, '完成 1 次种植'],
  [3, '使用 1 次肥料'],
  [4, '完成 1 次收获'],
  [5, '出售至少 1 个作物'],
])

const statusLabel = computed(() => {
  switch (props.chapter?.status) {
    case ChapterStatus.CLAIMABLE:
      return '奖励可领取'
    case ChapterStatus.CLAIMED:
      return '已领取'
    default:
      return '进行中'
  }
})
const canClaim = computed(
  () => props.connected && props.chapter?.status === ChapterStatus.CLAIMABLE,
)
</script>

<template>
  <div class="task-panel-body">
    <div class="panel-heading">
      <h3>第 {{ chapter?.chapterId ?? '—' }} 章</h3>
      <span class="state-pill">{{ statusLabel }}</span>
    </div>

    <ul v-if="chapter?.tasks.length" class="task-rows">
      <li v-for="task in chapter.tasks" :key="task.taskId" class="task-line">
        <img v-if="task.completed" class="pixel-art" :src="checkIcon" alt="完成" />
        <span v-else class="task-dot" aria-hidden="true"></span>
        <div>
          <strong>{{ taskNames.get(task.taskId) ?? `任务 ${task.taskId}` }}</strong>
          <small>{{ task.currentValue }} / {{ task.targetValue }}</small>
        </div>
        <progress :value="task.currentValue" :max="task.targetValue"></progress>
      </li>
    </ul>
    <p v-else class="empty-state">第二章内容尚未配置，第一章奖励已经领取。</p>

    <button
      v-if="chapter?.status === ChapterStatus.CLAIMABLE"
      class="primary"
      type="button"
      :disabled="!canClaim || Boolean(busyAction)"
      @click="emit('action', { action: 'claim' })"
    >
      {{ busyAction?.action === 'claim' ? '领取中…' : '领取奖励：10 金币、肥料和下一章种子' }}
    </button>
  </div>
</template>

<style scoped>
.task-panel-body {
  display: grid;
  gap: 0.7rem;
}

.task-panel-body h3 {
  margin: 0;
  color: #24361f;
  font-size: 0.95rem;
}

.task-rows {
  display: grid;
  gap: 0.5rem;
  margin: 0;
  padding: 0;
  list-style: none;
}

.task-line {
  display: grid;
  grid-template-columns: 1.2rem 1fr;
  gap: 0.35rem 0.5rem;
  padding: 0.6rem;
  border: 1px solid #c3b48e;
  border-radius: 0.65rem;
  background: #fffdf2;
}

.task-line img {
  width: 1.2rem;
  height: 1.2rem;
}

.task-line div {
  display: grid;
  gap: 0.15rem;
}

.task-line small {
  color: #6d755f;
  font-size: 0.68rem;
}

.task-line progress {
  grid-column: 1 / 3;
  width: 100%;
}
</style>
