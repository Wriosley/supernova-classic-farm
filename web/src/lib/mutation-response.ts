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
    case 'buyPetResponse':
    case 'deployPetResponse':
    case 'buyPetFoodResponse':
    case 'feedPetResponse':
    case 'plantResponse':
    case 'applyFertilizerResponse':
    case 'harvestResponse':
    case 'sellCropResponse':
    case 'claimChapterRewardResponse':
    case 'cleanPlotResponse':
    case 'catchPestResponse':
    case 'sendFriendGiftResponse':
    case 'claimMailResponse':
      return response.payload.value.patch
    default:
      return undefined
  }
}
