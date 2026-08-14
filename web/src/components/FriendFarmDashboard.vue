<script setup lang="ts">
import { computed, ref } from 'vue'
import type {
  CropCatalogEntryView,
  FarmVisitSnapshot,
  PublicPlotView,
} from '../gen/classicfarm/v1/ws/ws_pb'
import { PlotState } from '../gen/classicfarm/v1/ws/plot/plot_state_pb'
import { matureCropSprite } from '../lib/crop-art'
import { deployedPetFromPublic } from '../lib/pet-art'
import type { PlotFloat } from '../lib/plot-floats'
import FarmPetBadge from './FarmPetBadge.vue'

import plotEmpty from '../../../frontend/src/assets/art/runtime/plots/empty.png'
import plotGrowing from '../../../frontend/src/assets/art/runtime/plots/growing.png'
import plotMature from '../../../frontend/src/assets/art/runtime/plots/mature.png'
import plotCleanup from '../../../frontend/src/assets/art/runtime/plots/need-cleanup.png'
import cropGrowing from '../../../frontend/src/assets/art/runtime/crops/demo-growing.png'
import handTool from '../../../frontend/src/assets/art/runtime/tools/hand.png'
import shovelTool from '../../../frontend/src/assets/art/runtime/tools/shovel.png'
import fertilizerTool from '../../../frontend/src/assets/art/runtime/tools/fertilizer.png'
import seedTool from '../../../frontend/src/assets/art/runtime/tools/seed.png'

type VisitTool = 'pest' | 'catch' | 'steal' | 'clean'

const props = defineProps<{
  snapshot?: FarmVisitSnapshot
  ownerLabel: string
  cropCatalog: CropCatalogEntryView[]
  connected: boolean
  busy: boolean
  stealBusyPlotId?: number
  nowMs: bigint
  plotFloats?: PlotFloat[]
}>()

const emit = defineEmits<{
  steal: [plotId: number]
  pest: [plotId: number]
  catch: [plotId: number]
  clean: [plotId: number]
  plotFeedback: [plotId: number, text: string]
  exit: []
  openProfile: []
}>()

const selectedTool = ref<VisitTool>('steal')

const plots = computed(() =>
  [...(props.snapshot?.plots ?? [])].sort((left, right) => left.plotId - right.plotId),
)
const ownerPet = computed(() => deployedPetFromPublic(props.snapshot?.pet))

const toolOptions: Array<{ id: VisitTool; label: string; icon: string; hint: string }> = [
  { id: 'pest', label: '投虫', icon: seedTool, hint: '成长中且无虫' },
  { id: 'catch', label: '捉虫', icon: fertilizerTool, hint: '有害虫的地块' },
  { id: 'steal', label: '偷菜', icon: handTool, hint: '可偷的成熟地块' },
  { id: 'clean', label: '清理', icon: shovelTool, hint: '待清理地块' },
]

function cropNameById(cropId: number): string {
  return props.cropCatalog.find((crop) => crop.cropId === cropId)?.name ?? `作物#${cropId}`
}

function floatsFor(plotId: number): PlotFloat[] {
  return (props.plotFloats ?? []).filter((float) => float.plotId === plotId)
}

function canApplyPest(plot: PublicPlotView): boolean {
  return plot.plotState === PlotState.GROWING && !plot.pestActive
}

function canCatchPest(plot: PublicPlotView): boolean {
  return plot.plotState === PlotState.GROWING && plot.pestActive
}

function canHelpClean(plot: PublicPlotView): boolean {
  return plot.plotState === PlotState.NEED_CLEANUP
}

function isValidTarget(plot: PublicPlotView): boolean {
  switch (selectedTool.value) {
    case 'pest':
      return canApplyPest(plot)
    case 'catch':
      return canCatchPest(plot)
    case 'steal':
      return plot.canSteal
    case 'clean':
      return canHelpClean(plot)
  }
}

function formatCountdown(seconds: number): string {
  const safe = Math.max(0, seconds)
  const mins = Math.floor(safe / 60)
  const secs = safe % 60
  return `${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}`
}

function plotPresentation(plot: PublicPlotView) {
  const name = plot.cropId > 0 ? cropNameById(plot.cropId) : ''
  switch (plot.plotState) {
    case PlotState.GROWING:
      return {
        label: plot.pestActive ? `${name}成长中 · 有虫` : `${name}成长中`,
        base: plotGrowing,
        crop: cropGrowing,
      }
    case PlotState.MATURE:
      return {
        label: plot.canSteal ? `${name}可偷` : `${name}已成熟`,
        base: plotMature,
        crop: matureCropSprite(plot.cropId),
      }
    case PlotState.NEED_CLEANUP:
      return { label: `${name || '作物'}待清理`, base: plotCleanup, crop: undefined }
    default:
      return { label: '空地', base: plotEmpty, crop: undefined }
  }
}

function estimatedSeconds(plot: PublicPlotView): number {
  if (!plot.estimatedMatureAtMs || plot.estimatedMatureAtMs <= props.nowMs) {
    return 0
  }
  return Number((plot.estimatedMatureAtMs - props.nowMs + 999n) / 1000n)
}

function plotMeta(plot: PublicPlotView): string {
  const parts: string[] = []
  if (isValidTarget(plot)) {
    switch (selectedTool.value) {
      case 'pest':
        parts.push('点击投虫')
        break
      case 'catch':
        parts.push('点击捉虫')
        break
      case 'steal':
        parts.push('点击偷菜')
        break
      case 'clean':
        parts.push('点击帮忙清理')
        break
    }
  } else if (plot.plotState === PlotState.GROWING) {
    const seconds = estimatedSeconds(plot)
    parts.push(seconds > 0 ? `成熟倒计时：${formatCountdown(seconds)}` : '即将成熟')
  } else if (plot.plotState === PlotState.MATURE) {
    parts.push(plot.canSteal ? '可偷' : '本批作物不可偷')
  } else if (plot.plotState === PlotState.NEED_CLEANUP) {
    parts.push('已收获，待清理')
  } else {
    parts.push('空地')
  }
  if (plot.plotState === PlotState.MATURE) {
    parts.push(`剩余 ${plot.harvestableQuantity} 个`)
  }
  if (plot.pestActive) {
    parts.push('有害虫')
  }
  return parts.join(' · ')
}

function clickPlot(plot: PublicPlotView): void {
  if (!props.connected || props.busy || props.stealBusyPlotId !== undefined) {
    emit(
      'plotFeedback',
      plot.plotId,
      props.connected ? '上一项操作仍在处理中。' : '实时连接已断开。',
    )
    return
  }
  if (!isValidTarget(plot)) {
    const text =
      selectedTool.value === 'pest'
        ? plot.plotState !== PlotState.GROWING
          ? '只能对成长中的作物投虫。'
          : '这块地已经有害虫。'
        : selectedTool.value === 'catch'
          ? plot.plotState !== PlotState.GROWING
            ? '只能给成长中的作物捉虫。'
            : '这块地没有害虫。'
          : selectedTool.value === 'steal'
            ? '这块地现在不能偷。'
            : '这块地现在不能清理。'
    emit('plotFeedback', plot.plotId, text)
    return
  }
  switch (selectedTool.value) {
    case 'pest':
      emit('pest', plot.plotId)
      break
    case 'catch':
      emit('catch', plot.plotId)
      break
    case 'steal':
      emit('steal', plot.plotId)
      break
    case 'clean':
      emit('clean', plot.plotId)
      break
  }
}
</script>

<template>
  <section class="farm-dashboard friend-farm-dashboard" aria-label="好友农场">
    <header class="farm-toolbar">
      <div>
        <p class="eyebrow">FRIEND FARM · PUBLIC PLOTS</p>
        <h2>{{ ownerLabel }} 的农场</h2>
      </div>
      <div class="farm-toolbar__actions">
        <button type="button" @click="emit('openProfile')">查看好友资料</button>
        <button type="button" class="primary" :disabled="busy" @click="emit('exit')">
          离开农场
        </button>
      </div>
    </header>

    <nav class="toolbelt game-panel" aria-label="串门工具栏">
      <div>
        <span class="panel-kicker">VISIT MODE</span>
        <h3>访客互动</h3>
      </div>
      <div class="tool-options">
        <button
          v-for="tool in toolOptions"
          :key="tool.id"
          type="button"
          class="tool-button"
          :class="{ selected: selectedTool === tool.id }"
          :aria-pressed="selectedTool === tool.id"
          @click="selectedTool = tool.id"
        >
          <img class="pixel-art" :src="tool.icon" alt="" />
          <span>{{ tool.label }}</span>
          <small>{{ tool.hint }}</small>
        </button>
      </div>
    </nav>

    <div class="farm-layout">
      <article class="game-panel plots-panel">
        <div class="panel-heading">
          <div>
            <span class="panel-kicker">FRIEND PLOTS</span>
            <h3>好友农田</h3>
          </div>
          <span class="state-pill">
            当前：{{ toolOptions.find((tool) => tool.id === selectedTool)?.label }}
          </span>
        </div>
        <div class="farm-yard">
          <div
            v-if="plots.length"
            class="plots-grid"
            :data-tool="selectedTool === 'steal' ? 'hand' : selectedTool === 'clean' ? 'shovel' : 'seed'"
          >
            <button
              v-for="plot in plots"
              :key="plot.plotId"
              type="button"
              class="plot-tile"
              :class="{
                busy: stealBusyPlotId === plot.plotId,
                valid: connected && !busy && stealBusyPlotId === undefined && isValidTarget(plot),
                invalid: connected && !isValidTarget(plot),
              }"
              :aria-label="`好友地块 ${plot.plotId}，${plotPresentation(plot).label}`"
              @click="clickPlot(plot)"
            >
              <span class="plot-number">
                PLOT {{ String(plot.plotId).padStart(2, '0') }}
                <em v-if="plot.canSteal" class="steal-badge">可偷</em>
                <em v-else-if="plot.pestActive" class="steal-badge pest-badge">有虫</em>
              </span>
              <span class="plot-stage" :data-state="plot.plotState">
                <img class="plot-base pixel-art" :src="plotPresentation(plot).base" alt="" />
                <img
                  v-if="plotPresentation(plot).crop"
                  class="plot-crop pixel-art"
                  :src="plotPresentation(plot).crop"
                  alt=""
                />
              </span>
              <span class="plot-caption">
                <strong>{{ plotPresentation(plot).label }}</strong>
                <small>{{ plotMeta(plot) }}</small>
              </span>
              <span v-if="stealBusyPlotId === plot.plotId" class="plot-busy">处理中…</span>
              <span v-if="floatsFor(plot.plotId).length" class="plot-floats" aria-hidden="true">
                <span
                  v-for="float in floatsFor(plot.plotId)"
                  :key="float.id"
                  class="plot-float"
                  :class="float.tone"
                >
                  {{ float.text }}
                </span>
              </span>
            </button>
          </div>
          <p v-else class="empty-state">好友农场快照尚未加载。</p>
          <FarmPetBadge :pet="ownerPet" :now-ms="nowMs" />
        </div>
      </article>
    </div>
  </section>
</template>

<style scoped>
.friend-farm-dashboard .farm-layout {
  grid-template-columns: 1fr;
}

.farm-toolbar__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem;
}

.farm-yard {
  display: flex;
  align-items: flex-end;
  justify-content: center;
  gap: 0.6rem;
}

@media (max-width: 720px) {
  .farm-yard {
    flex-wrap: wrap;
  }
}

.steal-badge {
  margin-left: 0.4rem;
  padding: 0.05rem 0.35rem;
  border-radius: 99rem;
  background: #527b46;
  color: #f7f3e2;
  font-style: normal;
  font-size: 0.6rem;
}

.pest-badge {
  background: #8a5a2b;
}
</style>
