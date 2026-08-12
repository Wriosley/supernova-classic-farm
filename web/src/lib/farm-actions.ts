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
  seedItemId?: number
  shopEntryId?: number
  cropItemId?: number
  priceVersion?: bigint
}
