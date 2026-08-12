import { beforeEach, describe, expect, it } from 'vitest'
import {
  captureInviteFriendCodeFromLocation,
  clearPendingFriendCode,
  loadPendingFriendCode,
  normalizeFriendCode,
  savePendingFriendCode,
} from '../lib/friend-invite'

describe('friend invite pending code', () => {
  beforeEach(() => {
    sessionStorage.clear()
  })

  it('normalizes and rejects empty or oversized codes', () => {
    expect(normalizeFriendCode('  abcd1234  ')).toBe('ABCD1234')
    expect(normalizeFriendCode('')).toBeUndefined()
    expect(normalizeFriendCode('x'.repeat(33))).toBeUndefined()
  })

  it('captures /invite/friend?code= into sessionStorage', () => {
    const code = captureInviteFriendCodeFromLocation(
      'http://localhost:5173/invite/friend?code=ab12cd34',
    )
    expect(code).toBe('AB12CD34')
    expect(loadPendingFriendCode()).toBe('AB12CD34')
  })

  it('ignores non-invite paths', () => {
    expect(
      captureInviteFriendCodeFromLocation('http://localhost:5173/?code=AB12CD34'),
    ).toBeUndefined()
    expect(loadPendingFriendCode()).toBeUndefined()
  })

  it('clears pending code after success path', () => {
    savePendingFriendCode('ZZYYXXWW')
    clearPendingFriendCode()
    expect(loadPendingFriendCode()).toBeUndefined()
  })
})
