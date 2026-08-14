<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { CropCatalogEntryView, PlayerSnapshot, PlotView } from '../gen/classicfarm/v1/ws/ws_pb'
import { PlotState } from '../gen/classicfarm/v1/ws/plot/plot_state_pb'
import type { FarmActionRequest } from '../lib/farm-actions'
import { matureCropSprite } from '../lib/crop-art'
import type { DeployedPet } from '../lib/pet-art'
import type { PlotFloat } from '../lib/plot-floats'
import FarmPetBadge from './FarmPetBadge.vue'

import plotEmpty from '../../../frontend/src/assets/art/runtime/plots/empty.png'
import plotGrowing from '../../../frontend/src/assets/art/runtime/plots/growing.png'
import plotMature from '../../../frontend/src/assets/art/runtime/plots/mature.png'
import plotCleanup from '../../../frontend/src/assets/art/runtime/plots/need-cleanup.png'
import cropGrowing from '../../../frontend/src/assets/art/runtime/crops/demo-growing.png'
import seedIcon from '../../../frontend/src/assets/art/runtime/items/demo-seed.png'
import effectIcon from '../../../frontend/src/assets/art/runtime/effects/fertilized.png'
import fertilizerTool from '../../../frontend/src/assets/art/runtime/tools/fertilizer.png'
import shovelTool from '../../../frontend/src/assets/art/runtime/tools/shovel.png'
import handTool from '../../../frontend/src/assets/art/runtime/tools/hand.png'

type FarmTool = 'seed' | 'fertilizer' | 'catch' | 'shovel' | 'hand'

const props = defineProps<{
  snapshot?: PlayerSnapshot
  cropCatalog: CropCatalogEntryView[]
  connected: boolean
  busyAction?: FarmActionRequest
  nowMs: bigint
  activePet?: DeployedPet
  plotFloats?: PlotFloat[]
  visitors?: string[]
}>()

const emit = defineEmits<{
  action: [request: FarmActionRequest]
  plotFeedback: [plotId: number, text: string]
  openShop: []
  openPet: []
  reloadCatalog: []
}>()

const selectedTool = ref<FarmTool>('hand')
const selectedSeedCropId = ref(0)

const plots = computed(() => [...(props.snapshot?.plots ?? [])].sort((a, b) => a.plotId - b.plotId))
const inventory = computed(() => {
  const quantities = new Map<number, number>()
  for (const item of props.snapshot?.inventory ?? []) {
    quantities.set(item.itemId, item.quantity)
  }
  return quantities
})
const fertilizerQuantity = computed(() => inventory.value.get(1) ?? 0)
const shopSeedCrops = computed(() => props.cropCatalog.filter((crop) => crop.seedShopEntryId > 0))
// The bar shows what the player can actually plant right now; seeds they do not
// own belong in the shop drawer, not in an empty basket on the farm.
const seedCrops = computed(() => shopSeedCrops.value.filter((crop) => seedQuantityOf(crop) > 0))
const selectedSeed = computed(() =>
  seedCrops.value.find((crop) => crop.cropId === selectedSeedCropId.value),
)
const seedQuantity = computed(() => {
  const seed = selectedSeed.value
  return seed ? inventory.value.get(seed.seedItemId) ?? 0 : 0
})
const tools = computed<Array<{ id: FarmTool; label: string; icon: string; quantity?: number }>>(
  () => [
    { id: 'hand', label: '手', icon: handTool },
    { id: 'shovel', label: '铲子', icon: shovelTool },
    { id: 'catch', label: '杀虫剂', icon: handTool },
    { id: 'fertilizer', label: '肥料', icon: fertilizerTool, quantity: fertilizerQuantity.value },
  ],
)
const currentToolLabel = computed(() => {
  if (selectedTool.value === 'seed') {
    return `${selectedSeed.value?.name ?? '作物'}种子`
  }
  return tools.value.find((tool) => tool.id === selectedTool.value)?.label ?? '手'
})

watch(
  seedCrops,
  (crops) => {
    if (crops.length === 0 || crops.some((crop) => crop.cropId === selectedSeedCropId.value)) {
      return
    }
    selectedSeedCropId.value = crops[0].cropId
  },
  { immediate: true },
)

function cropNameById(cropId: number): string {
  return props.cropCatalog.find((crop) => crop.cropId === cropId)?.name ?? `作物#${cropId}`
}

function floatsFor(plotId: number): PlotFloat[] {
  return (props.plotFloats ?? []).filter((float) => float.plotId === plotId)
}

// maturity_seconds arrives as uint64, so it is a BigInt: mixing it into the
// arithmetic below throws and takes the whole render down with it.
function formatDuration(input: number | bigint): string {
  const seconds = Number(input)
  if (!Number.isFinite(seconds) || seconds <= 0) {
    return '即时'
  }
  if (seconds < 60) {
    return `${seconds} 秒`
  }
  const mins = Math.floor(seconds / 60)
  const secs = seconds % 60
  return secs > 0 ? `${mins} 分 ${secs} 秒` : `${mins} 分`
}

function formatCountdown(seconds: number): string {
  const safe = Math.max(0, seconds)
  const mins = Math.floor(safe / 60)
  const secs = safe % 60
  return `${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}`
}

function seedQuantityOf(crop: CropCatalogEntryView): number {
  return inventory.value.get(crop.seedItemId) ?? 0
}

function seedTooltip(crop: CropCatalogEntryView): string {
  return `${crop.name}的成熟时间是 ${formatDuration(crop.maturitySeconds)}`
}

function plotPresentation(plot: PlotView) {
  const name = plot.cropId > 0 ? cropNameById(plot.cropId) : ''
  switch (plot.plotState) {
    case PlotState.GROWING:
      return {
        label: plot.pestEffect ? `${name}成长中 · 有虫` : `${name}成长中`,
        base: plotGrowing,
        crop: cropGrowing,
      }
    case PlotState.MATURE:
      return { label: `${name}已成熟`, base: plotMature, crop: matureCropSprite(plot.cropId) }
    case PlotState.NEED_CLEANUP:
      return { label: `${name || '作物'}待清理`, base: plotCleanup, crop: undefined }
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
    parts.push(seconds > 0 ? `成熟倒计时：${formatCountdown(seconds)}` : '等待服务器确认成熟')
    if (selectedTool.value === 'catch' && plot.pestEffect) {
      parts.push('点击杀虫')
    }
    return parts.join(' · ')
  }
  if (plot.plotState === PlotState.MATURE) {
    return `可收获 ${plot.harvestableQuantity} 个`
  }
  if (plot.plotState === PlotState.NEED_CLEANUP) {
    return '收获完成，等待铲子清理'
  }
  const seed = selectedSeed.value
  return selectedTool.value === 'seed' && seed ? `空地可种植（${seed.name}）` : '空地可种植'
}

function targetAction(plot: PlotView): FarmActionRequest | undefined {
  switch (selectedTool.value) {
    case 'seed': {
      const seed = selectedSeed.value
      if (plot.plotState === PlotState.EMPTY && seed && seedQuantity.value > 0) {
        return { action: 'plant', plotId: plot.plotId, seedItemId: seed.seedItemId }
      }
      emit(
        'plotFeedback',
        plot.plotId,
        plot.plotState !== PlotState.EMPTY
          ? '种子只能用于空地。'
          : seed
            ? '仓库里没有所选种子。'
            : '仓库里没有可用种子。',
      )
      return undefined
    }
    case 'fertilizer':
      if (
        plot.plotState === PlotState.GROWING &&
        !plot.fertilizerEffect &&
        fertilizerQuantity.value > 0
      ) {
        return { action: 'fertilize', plotId: plot.plotId }
      }
      emit(
        'plotFeedback',
        plot.plotId,
        plot.plotState !== PlotState.GROWING
          ? '肥料只能用于成长中的作物。'
          : plot.fertilizerEffect
            ? '该地块已有肥料效果。'
            : '仓库里没有肥料。',
      )
      return undefined
    case 'catch':
      if (plot.plotState === PlotState.GROWING && plot.pestEffect) {
        return { action: 'catch', plotId: plot.plotId }
      }
      emit(
        'plotFeedback',
        plot.plotId,
        plot.plotState !== PlotState.GROWING
          ? '只能对成长中的作物使用杀虫剂。'
          : '这块地没有害虫。',
      )
      return undefined
    case 'hand':
      if (plot.plotState === PlotState.MATURE) {
        return { action: 'harvest', plotId: plot.plotId }
      }
      emit('plotFeedback', plot.plotId, '还不能收获。')
      return undefined
    case 'shovel':
      if (plot.plotState === PlotState.NEED_CLEANUP) {
        return { action: 'clean', plotId: plot.plotId }
      }
      emit('plotFeedback', plot.plotId, '还不能清理。')
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
    emit(
      'plotFeedback',
      plot.plotId,
      props.connected ? '上一项操作仍在处理中。' : '实时连接已断开。',
    )
    return
  }
  const request = targetAction(plot)
  if (request) {
    emit('action', request)
  }
}

function selectTool(tool: FarmTool): void {
  selectedTool.value = tool
}

function selectSeed(crop: CropCatalogEntryView): void {
  selectedSeedCropId.value = crop.cropId
  selectedTool.value = 'seed'
}
</script>

<template>
  <section class="farm-stage" aria-label="我的农场">
    <aside v-if="visitors?.length" class="farm-visitors" aria-live="polite">
      <strong>访客</strong>
      <span v-for="visitor in visitors" :key="visitor">{{ visitor }} 进入农场</span>
    </aside>

    <div class="farm-yard">
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
          <span class="plot-caption">
            <strong>{{ plotPresentation(plot).label }}</strong>
            <small>{{ plotMeta(plot) }}</small>
          </span>
          <span v-if="busyAction?.plotId === plot.plotId" class="plot-busy">处理中…</span>
          <span
            v-if="plot.plotState === PlotState.MATURE || floatsFor(plot.plotId).length"
            class="plot-floats"
            aria-hidden="true"
          >
            <span v-if="plot.plotState === PlotState.MATURE" class="plot-float persistent">
              可以收获
            </span>
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

      <FarmPetBadge
        :pet="activePet"
        :now-ms="nowMs"
        interactive
        @click="emit('openPet')"
      />
    </div>

    <div class="farm-bars">
      <nav class="farm-bar" aria-label="工具栏">
        <span class="farm-bar__label">工具</span>
        <div class="farm-bar__items">
          <button
            v-for="tool in tools"
            :key="tool.id"
            type="button"
            class="bar-chip"
            :class="{ selected: selectedTool === tool.id }"
            :aria-pressed="selectedTool === tool.id"
            @click="selectTool(tool.id)"
          >
            <img class="pixel-art" :src="tool.icon" alt="" />
            <span>{{ tool.label }}</span>
            <small v-if="tool.quantity !== undefined">×{{ tool.quantity }}</small>
          </button>
        </div>
        <span class="farm-bar__current">当前：{{ currentToolLabel }}</span>
      </nav>

      <nav class="farm-bar" aria-label="种子栏">
        <span class="farm-bar__label">种子</span>
        <div class="farm-bar__items">
          <button
            v-for="crop in seedCrops"
            :key="crop.cropId"
            type="button"
            class="bar-chip seed-chip"
            :class="{ selected: selectedTool === 'seed' && selectedSeedCropId === crop.cropId }"
            :aria-pressed="selectedTool === 'seed' && selectedSeedCropId === crop.cropId"
            :aria-label="seedTooltip(crop)"
            @click="selectSeed(crop)"
          >
            <img class="pixel-art" :src="seedIcon" alt="" />
            <span>{{ crop.name }}</span>
            <small>×{{ seedQuantityOf(crop) }}</small>
            <span class="seed-chip__tip" role="tooltip">{{ seedTooltip(crop) }}</span>
          </button>
          <button
            v-if="shopSeedCrops.length === 0"
            type="button"
            class="bar-chip"
            @click="emit('reloadCatalog')"
          >
            种子目录未加载，点此重试
          </button>
          <span v-else-if="seedCrops.length === 0" class="farm-bar__empty">
            仓库里还没有种子
          </span>
        </div>
        <button type="button" class="farm-bar__shop" @click="emit('openShop')">去商店</button>
      </nav>
    </div>
  </section>
</template>

<style scoped>
.farm-visitors {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.35rem 0.55rem;
  padding: 0.45rem 0.7rem;
  border: 2px solid #71895e;
  border-radius: 0.75rem;
  background: #f4f8e8;
  color: #24361f;
  font-size: 0.78rem;
}

.farm-visitors span {
  padding-left: 0.55rem;
  border-left: 1px solid #a9ba92;
}

.farm-stage {
  display: grid;
  gap: 0.6rem;
}

.action-notice {
  margin: 0;
}

.farm-yard {
  display: flex;
  align-items: flex-end;
  justify-content: center;
  gap: 0.6rem;
}

.plots-grid {
  margin-top: 0;
}

@media (max-width: 720px) {
  .farm-yard {
    flex-wrap: wrap;
  }
}

.farm-bars {
  position: sticky;
  bottom: 0.5rem;
  display: grid;
  gap: 0.5rem;
}

.farm-bar {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.5rem 0.7rem;
  border: 2px solid #8b6c42;
  border-radius: 0.9rem;
  background: #fff8dc;
  box-shadow: 0 0.4rem 1rem rgb(44 58 34 / 16%);
}

.farm-bar__label {
  flex: none;
  color: #6b7c54;
  font-size: 0.68rem;
  font-weight: 800;
  letter-spacing: 0.1em;
}

.farm-bar__items {
  display: flex;
  flex: 1;
  flex-wrap: nowrap;
  gap: 0.4rem;
  overflow-x: auto;
  padding-bottom: 0.15rem;
}

.farm-bar__current {
  flex: none;
  color: #5d694f;
  font-size: 0.7rem;
  font-weight: 750;
}

.farm-bar__shop {
  flex: none;
  min-height: 2.2rem;
  padding: 0.3rem 0.6rem;
  font-size: 0.75rem;
}

.bar-chip {
  position: relative;
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: 0.3rem;
  min-height: 2.6rem;
  padding: 0.3rem 0.6rem;
  font-size: 0.78rem;
  font-weight: 700;
  white-space: nowrap;
}

.bar-chip img {
  width: 1.4rem;
  height: 1.4rem;
}

.bar-chip small {
  color: #6b745e;
  font-size: 0.68rem;
}

.bar-chip.selected {
  border-color: #31552d;
  background: #dfecc2;
  box-shadow: 0 0 0 3px rgb(49 85 45 / 15%);
}

.farm-bar__empty {
  flex: none;
  align-self: center;
  color: #6b745e;
  font-size: 0.75rem;
  font-weight: 700;
}

.seed-chip__tip {
  position: absolute;
  bottom: calc(100% + 0.35rem);
  left: 50%;
  z-index: 10;
  padding: 0.3rem 0.5rem;
  border-radius: 0.45rem;
  background: rgb(36 54 31 / 92%);
  color: #f7f6ec;
  font-size: 0.7rem;
  font-weight: 600;
  opacity: 0;
  pointer-events: none;
  transform: translateX(-50%);
  transition: opacity 120ms ease;
  white-space: nowrap;
}

.seed-chip:hover .seed-chip__tip,
.seed-chip:focus-visible .seed-chip__tip {
  opacity: 1;
}
</style>
