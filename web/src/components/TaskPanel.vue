<script setup lang="ts">
import { computed, ref, watch } from 'vue'
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

type TaskDefinition = { taskId: number; name: string; targetValue: number }
type DisplayTask = TaskDefinition & { currentValue: number; completed: boolean }

const chapterDefinitions = new Map<number, TaskDefinition[]>([
  [1, [
    { taskId: 1, name: '购买 3 粒种子', targetValue: 3 },
    { taskId: 2, name: '完成 1 次种植', targetValue: 1 },
    { taskId: 3, name: '使用 1 次肥料', targetValue: 1 },
    { taskId: 4, name: '完成 1 次收获', targetValue: 1 },
    { taskId: 5, name: '出售至少 1 个作物', targetValue: 1 },
  ]],
  [2, [
    { taskId: 6, name: '添加 1 位好友', targetValue: 1 },
    { taskId: 7, name: '偷取 1 次好友作物', targetValue: 1 },
    { taskId: 8, name: '给好友农场投虫 1 次', targetValue: 1 },
  ]],
])

const selectedChapterId = ref(1)
watch(
  () => props.chapter?.chapterId,
  (chapterId) => {
    selectedChapterId.value = chapterId === 2 ? 2 : 1
  },
  { immediate: true },
)

const displayedTasks = computed<DisplayTask[]>(() => {
  const definitions = chapterDefinitions.get(selectedChapterId.value) ?? []
  if (props.chapter?.chapterId === selectedChapterId.value) {
    const progress = new Map(props.chapter.tasks.map((task) => [task.taskId, task]))
    return definitions.map((definition) => {
      const task = progress.get(definition.taskId)
      return {
        ...definition,
        currentValue: task?.currentValue ?? 0,
        targetValue: task?.targetValue ?? definition.targetValue,
        completed: task?.completed ?? false,
      }
    })
  }
  const completed = (props.chapter?.chapterId ?? 1) > selectedChapterId.value
  return definitions.map((definition) => ({
    ...definition,
    currentValue: completed ? definition.targetValue : 0,
    completed,
  }))
})

const statusLabel = computed(() => {
  if (selectedChapterId.value !== props.chapter?.chapterId) {
    return selectedChapterId.value < (props.chapter?.chapterId ?? 1) ? '已领取' : '尚未解锁'
  }
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
  () => props.connected && selectedChapterId.value === props.chapter?.chapterId &&
    props.chapter?.status === ChapterStatus.CLAIMABLE,
)
const terminalClaimed = computed(
  () => selectedChapterId.value === 2 && props.chapter?.chapterId === 2 &&
    props.chapter.status === ChapterStatus.CLAIMED,
)
const rewardLabel = computed(() => selectedChapterId.value === 1
  ? '领取奖励：10 金币、1 个肥料、3 个南瓜种子'
  : '领取奖励：10 金币、5 个肥料、10 个西瓜种子')
</script>

<template>
  <div class="task-panel-body">
    <div class="panel-heading">
      <h3>第 {{ selectedChapterId }} 章</h3>
      <span class="state-pill">{{ statusLabel }}</span>
    </div>

    <nav class="chapter-pages" aria-label="任务章节">
      <button type="button" :disabled="selectedChapterId === 1" @click="selectedChapterId = 1">
        上一章
      </button>
      <strong>{{ selectedChapterId }} / 2</strong>
      <button type="button" :disabled="selectedChapterId === 2" @click="selectedChapterId = 2">
        下一章
      </button>
    </nav>

    <ul class="task-rows">
      <li v-for="task in displayedTasks" :key="task.taskId" class="task-line">
        <img v-if="task.completed" class="pixel-art" :src="checkIcon" alt="完成" />
        <span v-else class="task-dot" aria-hidden="true"></span>
        <div>
          <strong>{{ task.name }}</strong>
          <small>{{ task.currentValue }} / {{ task.targetValue }}</small>
        </div>
        <progress :value="task.currentValue" :max="task.targetValue"></progress>
      </li>
    </ul>
    <p v-if="terminalClaimed" class="empty-state">奖励已领取，暂时没有更多任务了。</p>

    <button
      v-if="selectedChapterId === chapter?.chapterId && chapter?.status === ChapterStatus.CLAIMABLE"
      class="primary"
      type="button"
      :disabled="!canClaim || Boolean(busyAction)"
      @click="emit('action', { action: 'claim' })"
    >
      {{ busyAction?.action === 'claim' ? '领取中…' : rewardLabel }}
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

.chapter-pages {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  align-items: center;
  gap: 0.5rem;
}

.chapter-pages button:last-child {
  justify-self: end;
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
