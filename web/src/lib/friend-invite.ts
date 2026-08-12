const PENDING_FRIEND_CODE_KEY = 'classic-farm:pending-friend-code'
// Friend codes are 8 chars from a restricted alphabet today; keep a loose upper
// bound so a malicious URL cannot stuff sessionStorage with megabytes.
const MAX_FRIEND_CODE_LENGTH = 32

export function captureInviteFriendCodeFromLocation(
  href: string = window.location.href,
): string | undefined {
  let url: URL
  try {
    url = new URL(href)
  } catch {
    return undefined
  }
  if (!url.pathname.endsWith('/invite/friend')) {
    return undefined
  }
  const code = normalizeFriendCode(url.searchParams.get('code') ?? '')
  if (!code) {
    return undefined
  }
  savePendingFriendCode(code)
  // Drop the query from the address bar so a later refresh does not look like
  // a fresh invite click, but keep the pending code in sessionStorage.
  if (typeof window !== 'undefined' && window.history?.replaceState) {
    window.history.replaceState({}, '', '/')
  }
  return code
}

export function normalizeFriendCode(raw: string): string | undefined {
  const code = raw.trim().toUpperCase()
  if (!code || code.length > MAX_FRIEND_CODE_LENGTH) {
    return undefined
  }
  return code
}

export function savePendingFriendCode(code: string): void {
  const normalized = normalizeFriendCode(code)
  if (!normalized) {
    return
  }
  sessionStorage.setItem(PENDING_FRIEND_CODE_KEY, normalized)
}

export function loadPendingFriendCode(): string | undefined {
  const raw = sessionStorage.getItem(PENDING_FRIEND_CODE_KEY)
  if (!raw) {
    return undefined
  }
  return normalizeFriendCode(raw)
}

export function clearPendingFriendCode(): void {
  sessionStorage.removeItem(PENDING_FRIEND_CODE_KEY)
}

export function describeRedeemFriendError(message: string): string {
  if (message.includes('FRIEND_CODE_EXPIRED') || message.includes('过期')) {
    return '好友码已过期'
  }
  if (message.includes('CANNOT_FRIEND_SELF') || message.includes('自己')) {
    return '不能添加自己'
  }
  if (message.includes('FRIEND_LIMIT') || message.includes('已满')) {
    return '好友列表已满'
  }
  if (message.includes('FRIEND_CODE_NOT_FOUND') || message.includes('找不到')) {
    return '好友码无效'
  }
  if (message.includes('NOT_MUTUAL') || message.includes('早已是好友')) {
    return message
  }
  return message
}
