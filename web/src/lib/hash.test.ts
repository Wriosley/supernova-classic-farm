import assert from 'node:assert/strict'
import test from 'node:test'
import { bytesEqual, sha256, verifySha256 } from './hash'

test('sha256 verifies the exact original bytes', async () => {
  const bytes = new TextEncoder().encode('classic-farm-config')
  const digest = await sha256(bytes)

  assert.equal(digest.byteLength, 32)
  await assert.doesNotReject(verifySha256(bytes, digest))
  await assert.rejects(
    verifySha256(new TextEncoder().encode('changed'), digest),
    /SHA-256/,
  )
})

test('bytesEqual checks all bytes and lengths', () => {
  assert.equal(bytesEqual(Uint8Array.of(1, 2), Uint8Array.of(1, 2)), true)
  assert.equal(bytesEqual(Uint8Array.of(1, 2), Uint8Array.of(1, 3)), false)
  assert.equal(bytesEqual(Uint8Array.of(1), Uint8Array.of(1, 0)), false)
})
