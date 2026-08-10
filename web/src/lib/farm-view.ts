// 公开农场增量 Patch 的客户端合并规则：与协议一致，不依赖广播历史持久化。
export type FarmViewDecision =
  | { action: 'apply' }
  | { action: 'ignore' }
  | { action: 'resync' }

export function decideFarmViewPatch(input: {
  hasCurrentSnapshot: boolean
  currentEpoch?: Uint8Array
  currentSeq?: bigint
  nextEpoch?: Uint8Array
  nextSeq?: bigint
}): FarmViewDecision {
  if (!input.hasCurrentSnapshot || !input.currentEpoch || input.currentSeq === undefined) {
    return { action: 'resync' }
  }
  if (!input.nextEpoch || input.nextSeq === undefined) {
    return { action: 'resync' }
  }
  if (!bytesEqual(input.nextEpoch, input.currentEpoch)) {
    return { action: 'resync' }
  }
  if (input.nextSeq <= input.currentSeq) {
    return { action: 'ignore' }
  }
  if (input.nextSeq !== input.currentSeq + 1n) {
    return { action: 'resync' }
  }
  return { action: 'apply' }
}

function bytesEqual(left: Uint8Array, right: Uint8Array): boolean {
  if (left.length !== right.length) {
    return false
  }
  for (let i = 0; i < left.length; i += 1) {
    if (left[i] !== right[i]) {
      return false
    }
  }
  return true
}
