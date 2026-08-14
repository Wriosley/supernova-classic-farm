export type PanelId =
  | 'account'
  | 'shop'
  | 'pet'
  | 'compendium'
  | 'friends'
  | 'mailbox'
  | 'tasks'
  | 'inventory'

export const panelTitles: Record<PanelId, string> = {
  account: '账号',
  shop: '商店',
  pet: '宠物',
  compendium: '图鉴',
  friends: '好友',
  mailbox: '邮箱',
  tasks: '任务',
  inventory: '仓库',
}

export const panelKickers: Record<PanelId, string> = {
  account: 'SESSION & DIAGNOSTICS',
  shop: 'SHOP',
  pet: 'PET',
  compendium: 'COMPENDIUM',
  friends: 'FRIENDS',
  mailbox: 'MAILBOX',
  tasks: 'CHAPTER',
  inventory: 'BARN',
}

export const panelOrder: PanelId[] = [
  'account',
  'shop',
  'pet',
  'compendium',
  'friends',
  'mailbox',
  'tasks',
  'inventory',
]
