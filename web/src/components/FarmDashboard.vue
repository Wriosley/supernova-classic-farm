<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type {
  PlayerSnapshot,
  PlotView,
  ShopEntryView,
} from '../gen/classicfarm/v1/ws/ws_pb'
import { ChapterStatus } from '../gen/classicfarm/v1/ws/chapter/chapter_status_pb'
import { PlotState } from '../gen/classicfarm/v1/ws/plot/plot_state_pb'

import plotEmpty from '../../../frontend/src/assets/art/runtime/plots/empty.png'
import plotGrowing from '../../../frontend/src/assets/art/runtime/plots/growing.png'
import plotMature from '../../../frontend/src/assets/art/runtime/plots/mature.png'
import plotCleanup from '../../../frontend/src/assets/art/runtime/plots/need-cleanup.png'
import cropGrowing from '../../../frontend/src/assets/art/runtime/crops/demo-growing.png'
import cropMature from '../../../frontend/src/assets/art/runtime/crops/demo-mature.png'
import seedIcon from '../../../frontend/src/assets/art/runtime/items/demo-seed.png'
import cropIcon from '../../../frontend/src/assets/art/runtime/items/demo-crop.png'
import fertilizerIcon from '../../../frontend/src/assets/art/runtime/items/fertilizer-basic.png'
import coinIcon from '../../../frontend/src/assets/art/runtime/items/coin.png'
import effectIcon from '../../../frontend/src/assets/art/runtime/effects/fertilized.png'
import checkIcon from '../../../frontend/src/assets/art/runtime/ui/check.png'
import seedTool from '../../../frontend/src/assets/art/runtime/tools/seed.png'
import fertilizerTool from '../../../frontend/src/assets/art/runtime/tools/fertilizer.png'
import shovelTool from '../../../frontend/src/assets/art/runtime/tools/shovel.png'
import handTool from '../../../frontend/src/assets/art/runtime/tools/hand.png'

export type FarmAction =
  | 'buy'
  | 'buy-fertilizer'
  | 'plant'
  | 'fertilize'
  | 'harvest'
  | 'sell'
  | 'claim'
  | 'clean'
  | 'catch'

export type FarmActionRequest = {
  action: FarmAction
  plotId?: number
  quantity?: number
  sellAll?: boolean
}

type FarmTool = 'seed' | 'fertilizer' | 'catch' | 'shovel' | 'hand'

const props = defineProps<{
  snapshot?: PlayerSnapshot
  shopEntries: ShopEntryView[]
  connected: boolean
  busyAction?: FarmActionRequest
  actionMessage: string
  actionError: string
  nowMs: bigint
}>()

const emit = defineEmits<{
  action: [request: FarmActionRequest]
}>()

const selectedTool = ref<FarmTool>('seed')
const buyQuantity = ref(3)
const fertilizerBuyQuantity = ref(1)
const sellQuantity = ref(1)
const localMessage = ref('')
const chapter = computed(() => props.snapshot?.currentChapter)
const plots = computed(() => [...(props.snapshot?.plots ?? [])].sort((a, b) => a.plotId - b.plotId))
const seedQuote = computed(() => props.shopEntries.find((entry) => entry.itemId === 1001))
const fertilizerQuote = computed(() => props.shopEntries.find((entry) => entry.itemId === 1))
const cropQuote = computed(() => props.shopEntries.find((entry) => entry.itemId === 1002))
const inventory = computed(() => {
  const quantities = new Map<number, number>()
  for (const item of props.snapshot?.inventory ?? []) {
    quantities.set(item.itemId, item.quantity)
  }
  return quantities
})
const seedQuantity = computed(() => inventory.value.get(1001) ?? 0)
const cropQuantity = computed(() => inventory.value.get(1002) ?? 0)
const fertilizerQuantity = computed(() => inventory.value.get(1) ?? 0)
const nextSeedQuantity = computed(() => inventory.value.get(1003) ?? 0)
const buyTotal = computed(() => (seedQuote.value?.unitPrice ?? 0n) * BigInt(buyQuantity.value))
const fertilizerBuyTotal = computed(
  () => (fertilizerQuote.value?.unitPrice ?? 0n) * BigInt(fertilizerBuyQuantity.value),
)
const sellTotal = computed(() => (cropQuote.value?.unitPrice ?? 0n) * BigInt(sellQuantity.value))
const chapterStatusLabel = computed(() => {
  switch (chapter.value?.status) {
    case ChapterStatus.CLAIMABLE:
      return '奖励可领取'
    case ChapterStatus.CLAIMED:
      return '已领取'
    default:
      return '进行中'
  }
})
const canBuy = computed(() => Boolean(
  props.connected &&
  seedQuote.value?.enabled &&
  props.snapshot &&
  buyQuantity.value >= 1 &&
  buyQuantity.value <= 50 &&
  seedQuantity.value + buyQuantity.value <= 300 &&
  props.snapshot.coinBalance >= buyTotal.value,
))
const canBuyFertilizer = computed(() => Boolean(
  props.connected &&
  fertilizerQuote.value?.enabled &&
  props.snapshot &&
  fertilizerBuyQuantity.value >= 1 &&
  fertilizerBuyQuantity.value <= 50 &&
  fertilizerQuantity.value + fertilizerBuyQuantity.value <= 300 &&
  props.snapshot.coinBalance >= fertilizerBuyTotal.value,
))
const canSell = computed(() => Boolean(
  props.connected &&
  cropQuote.value?.enabled &&
  sellQuantity.value >= 1 &&
  sellQuantity.value <= cropQuantity.value,
))
const canClaim = computed(
  () => props.connected && chapter.value?.status === ChapterStatus.CLAIMABLE,
)
const toolOptions = computed<Array<{ id: FarmTool; label: string; icon: string; quantity?: number }>>(
  () => [
    { id: 'seed', label: '种子', icon: seedTool, quantity: seedQuantity.value },
    { id: 'fertilizer', label: '肥料', icon: fertilizerTool, quantity: fertilizerQuantity.value },
    { id: 'catch', label: '捉虫', icon: handTool },
    { id: 'shovel', label: '铲子', icon: shovelTool },
    { id: 'hand', label: '手', icon: handTool },
  ],
)
const taskNames = new Map<number, string>([
  [1, '购买 3 粒种子'],
  [2, '完成 1 次种植'],
  [3, '使用 1 次肥料'],
  [4, '完成 1 次收获'],
  [5, '出售至少 1 个作物'],
])

watch(cropQuantity, (quantity) => {
  sellQuantity.value = quantity > 0 ? Math.min(Math.max(sellQuantity.value, 1), quantity) : 1
})

function clampBuy(): void {
  buyQuantity.value = Math.min(50, Math.max(1, Math.trunc(Number(buyQuantity.value) || 1)))
}

function clampFertilizerBuy(): void {
  fertilizerBuyQuantity.value = Math.min(
    50,
    Math.max(1, Math.trunc(Number(fertilizerBuyQuantity.value) || 1)),
  )
}

function clampSell(): void {
  sellQuantity.value = Math.min(
    Math.max(cropQuantity.value, 1),
    Math.max(1, Math.trunc(Number(sellQuantity.value) || 1)),
  )
}

function plotPresentation(plot: PlotView) {
  switch (plot.plotState) {
    case PlotState.GROWING:
      return {
        label: plot.pestEffect ? '成长中 · 有虫' : '成长中',
        base: plotGrowing,
        crop: cropGrowing,
      }
    case PlotState.MATURE:
      return { label: '可以收获', base: plotMature, crop: cropMature }
    case PlotState.NEED_CLEANUP:
      return { label: '等待清理', base: plotCleanup, crop: undefined }
    default:
      return { label: '空地', base: plotEmpty, crop: undefined }
  }
}

function estimatedSeconds(plot: PlotView): number {
  if (!plot.estimatedMatureAtMs || plot.estimatedMatureAtMs <= props.nowMs) {
    return 0
  }
  return Number((plot.estimatedMatureAtMs - props.nowMs + 999n) / 1000n)
}

function plotMeta(plot: PlotView): string {
  if (plot.plotState === PlotState.GROWING) {
    const parts: string[] = []
    if (plot.pestEffect) {
      parts.push('有害虫')
    }
    const seconds = estimatedSeconds(plot)
    parts.push(seconds > 0 ? `${seconds} 秒后成熟` : '等待服务器确认成熟')
    if (selectedTool.value === 'catch' && plot.pestEffect) {
      parts.push('点击捉虫')
    }
    return parts.join(' · ')
  }
  if (plot.plotState === PlotState.MATURE) {
    return `可收获 ${plot.harvestableQuantity} 个作物`
  }
  if (plot.plotState === PlotState.NEED_CLEANUP) {
    return '收获完成，等待铲子清理'
  }
  return '空地可种植'
}

function targetAction(plot: PlotView): FarmActionRequest | undefined {
  switch (selectedTool.value) {
    case 'seed':
      if (plot.plotState === PlotState.EMPTY && seedQuantity.value > 0) {
        return { action: 'plant', plotId: plot.plotId }
      }
      localMessage.value = plot.plotState !== PlotState.EMPTY ? '种子只能用于空地。' : '仓库里没有可用种子。'
      return undefined
    case 'fertilizer':
      if (
        plot.plotState === PlotState.GROWING &&
        !plot.fertilizerEffect &&
        fertilizerQuantity.value > 0
      ) {
        return { action: 'fertilize', plotId: plot.plotId }
      }
      localMessage.value =
        plot.plotState !== PlotState.GROWING
          ? '肥料只能用于成长中的作物。'
          : plot.fertilizerEffect
            ? '该地块已有肥料效果。'
            : '仓库里没有肥料。'
      return undefined
    case 'catch':
      if (plot.plotState === PlotState.GROWING && plot.pestEffect) {
        return { action: 'catch', plotId: plot.plotId }
      }
      localMessage.value =
        plot.plotState !== PlotState.GROWING
          ? '只能在成长中的作物上捉虫。'
          : '这块地没有害虫。'
      return undefined
    case 'hand':
      if (plot.plotState === PlotState.MATURE) {
        return { action: 'harvest', plotId: plot.plotId }
      }
      localMessage.value = '手只能收获已经成熟的作物。'
      return undefined
    case 'shovel':
      if (plot.plotState === PlotState.NEED_CLEANUP) {
        return { action: 'clean', plotId: plot.plotId }
      }
      localMessage.value = '铲子只能清理收获后的地块。'
      return undefined
  }
}

function isValidTarget(plot: PlotView): boolean {
  switch (selectedTool.value) {
    case 'seed':
      return plot.plotState === PlotState.EMPTY && seedQuantity.value > 0
    case 'fertilizer':
      return (
        plot.plotState === PlotState.GROWING &&
        !plot.fertilizerEffect &&
        fertilizerQuantity.value > 0
      )
    case 'catch':
      return plot.plotState === PlotState.GROWING && Boolean(plot.pestEffect)
    case 'hand':
      return plot.plotState === PlotState.MATURE
    case 'shovel':
      return plot.plotState === PlotState.NEED_CLEANUP
  }
}

function clickPlot(plot: PlotView): void {
  if (!props.connected || props.busyAction) {
    localMessage.value = props.connected ? '上一项操作仍在处理中。' : 'WebSocket 尚未连接。'
    return
  }
  const request = targetAction(plot)
  if (request) {
    localMessage.value = ''
    emit('action', request)
  }
}

function selectTool(tool: FarmTool): void {
  selectedTool.value = tool
  localMessage.value = ''
}

function run(request: FarmActionRequest): void {
  if (!props.busyAction) {
    localMessage.value = ''
    emit('action', request)
  }
}
</script>

<template>
  <section class="farm-dashboard" aria-label="经典农场">
    <header class="farm-toolbar">
      <div>
        <p class="eyebrow">PLAYER FARM · FOUR AUTHORITATIVE PLOTS</p>
        <h2>我的农场</h2>
      </div>
      <div class="wallet">
        <img :src="coinIcon" alt="" />
        <strong>{{ snapshot?.coinBalance.toString() ?? '—' }}</strong>
        <span>金币</span>
      </div>
    </header>

    <p v-if="actionError" class="action-notice error-banner" role="alert">{{ actionError }}</p>
    <p v-else-if="localMessage" class="action-notice tool-feedback" role="status">{{ localMessage }}</p>
    <p v-else-if="actionMessage" class="action-notice success-banner" role="status">
      {{ actionMessage }}
    </p>

    <nav class="toolbelt game-panel" aria-label="农场工具栏">
      <div>
        <span class="panel-kicker">TOOLBELT</span>
        <h3>选择工具，再点击地块</h3>
      </div>
      <div class="tool-options">
        <button
          v-for="tool in toolOptions"
          :key="tool.id"
          type="button"
          class="tool-button"
          :class="{ selected: selectedTool === tool.id }"
          :aria-pressed="selectedTool === tool.id"
          @click="selectTool(tool.id)"
        >
          <img class="pixel-art" :src="tool.icon" alt="" />
          <span>{{ tool.label }}</span>
          <small v-if="tool.quantity !== undefined">×{{ tool.quantity }}</small>
        </button>
      </div>
    </nav>

    <div class="farm-layout">
      <article class="game-panel plots-panel">
        <div class="panel-heading">
          <div>
            <span class="panel-kicker">PLOTS 01–04</span>
            <h3>农田</h3>
          </div>
          <span class="state-pill">当前工具：{{ toolOptions.find((tool) => tool.id === selectedTool)?.label }}</span>
        </div>
        <div class="plots-grid" :data-tool="selectedTool">
          <button
            v-for="plot in plots"
            :key="plot.plotId"
            type="button"
            class="plot-tile"
            :class="{
              busy: busyAction?.plotId === plot.plotId,
              valid: !busyAction && connected && isValidTarget(plot),
              invalid: !busyAction && connected && !isValidTarget(plot),
            }"
            :aria-label="`地块 ${plot.plotId}，${plotPresentation(plot).label}`"
            @click="clickPlot(plot)"
          >
            <span class="plot-number">
              PLOT {{ String(plot.plotId).padStart(2, '0') }}
              <em v-if="plot.pestEffect" class="pest-badge">有虫</em>
            </span>
            <span class="plot-stage" :data-state="plot.plotState">
              <img class="plot-base pixel-art" :src="plotPresentation(plot).base" alt="" />
              <img
                v-if="plotPresentation(plot).crop"
                class="plot-crop pixel-art"
                :src="plotPresentation(plot).crop"
                alt=""
              />
              <img
                v-if="plot.fertilizerEffect"
                class="plot-effect pixel-art"
                :src="effectIcon"
                alt="肥料效果"
              />
            </span>
            <strong>{{ plotPresentation(plot).label }}</strong>
            <small>{{ plotMeta(plot) }}</small>
            <span v-if="busyAction?.plotId === plot.plotId" class="plot-busy">处理中…</span>
          </button>
        </div>
      </article>

      <div class="farm-sidebar">
        <article class="game-panel shop-panel">
          <div class="panel-heading">
            <div>
              <span class="panel-kicker">SHOP</span>
              <h3>商店</h3>
            </div>
            <span v-if="seedQuote" class="price-tag">{{ seedQuote.unitPrice }} 金币 / 粒</span>
          </div>
          <div class="shop-item">
            <img class="item-icon pixel-art" :src="seedIcon" alt="演示种子" />
            <div class="shop-copy">
              <strong>演示作物种子</strong>
              <small>单次购买 1–50 粒 · 仓库堆叠上限 300</small>
            </div>
          </div>
          <div class="shop-item">
            <img class="item-icon pixel-art" :src="fertilizerIcon" alt="基础肥料" />
            <div class="shop-copy">
              <strong>基础肥料</strong>
              <small>每袋 {{ fertilizerQuote?.unitPrice ?? '—' }} 金币 · 仓库堆叠上限 300</small>
            </div>
          </div>
          <div class="quantity-row">
            <button type="button" aria-label="减少肥料购买数量" @click="fertilizerBuyQuantity--; clampFertilizerBuy()">−</button>
            <input
              v-model.number="fertilizerBuyQuantity"
              type="number"
              inputmode="numeric"
              min="1"
              max="50"
              aria-label="肥料购买数量"
              @change="clampFertilizerBuy"
            />
            <button type="button" aria-label="增加肥料购买数量" @click="fertilizerBuyQuantity++; clampFertilizerBuy()">＋</button>
            <span>合计 {{ fertilizerBuyTotal }} 金币</span>
            <button
              class="primary"
              type="button"
              :disabled="!canBuyFertilizer || Boolean(busyAction)"
              @click="run({ action: 'buy-fertilizer', quantity: fertilizerBuyQuantity })"
            >
              {{ busyAction?.action === 'buy-fertilizer' ? '购买中…' : `购买 ${fertilizerBuyQuantity} 袋` }}
            </button>
          </div>
          <div class="quantity-row">
            <button type="button" aria-label="减少购买数量" @click="buyQuantity--; clampBuy()">−</button>
            <input
              v-model.number="buyQuantity"
              type="number"
              inputmode="numeric"
              min="1"
              max="50"
              aria-label="购买数量"
              @change="clampBuy"
            />
            <button type="button" aria-label="增加购买数量" @click="buyQuantity++; clampBuy()">＋</button>
            <span>合计 {{ buyTotal }} 金币</span>
            <button
              class="primary"
              type="button"
              :disabled="!canBuy || Boolean(busyAction)"
              @click="run({ action: 'buy', quantity: buyQuantity })"
            >
              {{ busyAction?.action === 'buy' ? '购买中…' : `购买 ${buyQuantity} 粒` }}
            </button>
          </div>
        </article>

        <article class="game-panel inventory-panel">
          <div class="panel-heading">
            <div>
              <span class="panel-kicker">BARN</span>
              <h3>仓库</h3>
            </div>
          </div>
          <div class="inventory-grid">
            <div class="inventory-slot">
              <img class="pixel-art" :src="seedIcon" alt="" />
              <span>种子</span><strong>× {{ seedQuantity }}</strong>
            </div>
            <div class="inventory-slot">
              <img class="pixel-art" :src="fertilizerIcon" alt="" />
              <span>肥料</span><strong>× {{ fertilizerQuantity }}</strong>
            </div>
            <div class="inventory-slot">
              <img class="pixel-art" :src="cropIcon" alt="" />
              <span>作物</span><strong>× {{ cropQuantity }}</strong>
            </div>
            <div class="inventory-slot">
              <img class="pixel-art" :src="seedIcon" alt="" />
              <span>下一章种子</span><strong>× {{ nextSeedQuantity }}</strong>
            </div>
          </div>
          <div class="sell-controls">
            <span>出售作物</span>
            <div class="quantity-row">
              <button type="button" aria-label="减少出售数量" @click="sellQuantity--; clampSell()">−</button>
              <input
                v-model.number="sellQuantity"
                type="number"
                inputmode="numeric"
                min="1"
                :max="Math.max(cropQuantity, 1)"
                aria-label="出售数量"
                @change="clampSell"
              />
              <button type="button" aria-label="增加出售数量" @click="sellQuantity++; clampSell()">＋</button>
              <span>预计 {{ sellTotal }} 金币</span>
            </div>
            <div class="sell-buttons">
              <button
                type="button"
                :disabled="!canSell || Boolean(busyAction)"
                @click="run({ action: 'sell', quantity: sellQuantity })"
              >
                出售 {{ sellQuantity }} 个
              </button>
              <button
                type="button"
                :disabled="cropQuantity < 1 || !connected || Boolean(busyAction)"
                @click="run({ action: 'sell', sellAll: true })"
              >
                {{ busyAction?.action === 'sell' ? '出售中…' : '全部出售' }}
              </button>
            </div>
          </div>
        </article>
      </div>
    </div>

    <article class="game-panel chapter-panel">
      <div class="panel-heading">
        <div>
          <span class="panel-kicker">CHAPTER {{ chapter?.chapterId ?? '—' }}</span>
          <h3>章节任务</h3>
        </div>
        <span class="state-pill">{{ chapterStatusLabel }}</span>
      </div>
      <div v-if="chapter?.tasks.length" class="task-list">
        <div v-for="task in chapter.tasks" :key="task.taskId" class="task-row">
          <img v-if="task.completed" class="pixel-art" :src="checkIcon" alt="完成" />
          <span v-else class="task-dot" aria-hidden="true"></span>
          <div>
            <strong>{{ taskNames.get(task.taskId) ?? `任务 ${task.taskId}` }}</strong>
            <small>{{ task.currentValue }} / {{ task.targetValue }}</small>
          </div>
          <progress :value="task.currentValue" :max="task.targetValue"></progress>
        </div>
      </div>
      <p v-else class="chapter-placeholder">第二章内容尚未配置，第一章奖励已经领取。</p>
      <button
        v-if="chapter?.status === ChapterStatus.CLAIMABLE"
        class="primary claim-button"
        type="button"
        :disabled="!canClaim || Boolean(busyAction)"
        @click="run({ action: 'claim' })"
      >
        {{ busyAction?.action === 'claim' ? '领取中…' : '领取奖励：10 金币、肥料和下一章种子' }}
      </button>
    </article>
  </section>
</template>
