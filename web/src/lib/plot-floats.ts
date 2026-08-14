import { onScopeDispose, ref, type Ref } from 'vue'

export type PlotFloatTone = 'success' | 'error'

export interface PlotFloat {
  id: number
  plotId: number
  text: string
  tone: PlotFloatTone
}

export interface PlotFloatController {
  floats: Ref<PlotFloat[]>
  pushFloat: (plotId: number, text: string, tone?: PlotFloatTone) => void
  clearFloats: () => void
}

const DEFAULT_LIFETIME_MS = 1600

export function usePlotFloats(lifetimeMs = DEFAULT_LIFETIME_MS): PlotFloatController {
  const floats = ref<PlotFloat[]>([])
  const timers = new Set<ReturnType<typeof setTimeout>>()
  let nextId = 1

  function clearFloats(): void {
    for (const timer of timers) {
      clearTimeout(timer)
    }
    timers.clear()
    floats.value = []
  }

  function pushFloat(plotId: number, text: string, tone: PlotFloatTone = 'success'): void {
    if (!plotId || !text) {
      return
    }
    const id = nextId++
    floats.value = [...floats.value, { id, plotId, text, tone }]
    const timer = setTimeout(() => {
      timers.delete(timer)
      floats.value = floats.value.filter((float) => float.id !== id)
    }, lifetimeMs)
    timers.add(timer)
  }

  onScopeDispose(clearFloats)

  return { floats, pushFloat, clearFloats }
}
