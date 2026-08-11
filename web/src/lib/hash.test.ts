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

test('sha256 matches WebCrypto without a secure context', async () => {
  const webCrypto = globalThis.crypto
  const cases = ['', 'a', 'classic-farm-config', 'x'.repeat(1000)]
  const expected = await Promise.all(
    cases.map((text) => sha256(new TextEncoder().encode(text))),
  )

  Object.defineProperty(globalThis, 'crypto', {
    configurable: true,
    value: { getRandomValues: webCrypto.getRandomValues.bind(webCrypto) },
  })
  try {
    for (let index = 0; index < cases.length; index += 1) {
      const actual = await sha256(new TextEncoder().encode(cases[index]))
      assert.deepEqual(actual, expected[index])
    }
  } finally {
    Object.defineProperty(globalThis, 'crypto', {
      configurable: true,
      value: webCrypto,
    })
  }
})

test('bytesEqual checks all bytes and lengths', () => {
  assert.equal(bytesEqual(Uint8Array.of(1, 2), Uint8Array.of(1, 2)), true)
  assert.equal(bytesEqual(Uint8Array.of(1, 2), Uint8Array.of(1, 3)), false)
  assert.equal(bytesEqual(Uint8Array.of(1), Uint8Array.of(1, 0)), false)
})
