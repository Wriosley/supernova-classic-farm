import type {
  PlayerStatePatch,
  WsEnvelope,
} from '../gen/classicfarm/v1/ws/ws_pb'

export function mutationResponsePatch(
  response: WsEnvelope,
): PlayerStatePatch | undefined {
  switch (response.payload.case) {
    case 'buySeedsResponse':
    case 'buyFertilizerResponse':
    case 'plantResponse':
    case 'applyFertilizerResponse':
    case 'harvestResponse':
    case 'sellCropResponse':
    case 'claimChapterRewardResponse':
    case 'cleanPlotResponse':
      return response.payload.value.patch
    default:
      return undefined
  }
}
