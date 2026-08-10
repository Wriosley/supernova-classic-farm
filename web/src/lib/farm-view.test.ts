import assert from 'node:assert/strict'
import test from 'node:test'
import { decideFarmViewPatch } from './farm-view'

const epochA = new Uint8Array([1, 2, 3, 4])
const epochB = new Uint8Array([9, 8, 7, 6])

test('applies contiguous next seq', () => {
  assert.deepEqual(
    decideFarmViewPatch({
      hasCurrentSnapshot: true,
      currentEpoch: epochA,
      currentSeq: 17n,
      nextEpoch: epochA,
      nextSeq: 18n,
    }),
    { action: 'apply' },
  )
})

test('ignores duplicate or older seq', () => {
  assert.deepEqual(
    decideFarmViewPatch({
      hasCurrentSnapshot: true,
      currentEpoch: epochA,
      currentSeq: 18n,
      nextEpoch: epochA,
      nextSeq: 18n,
    }),
    { action: 'ignore' },
  )
  assert.deepEqual(
    decideFarmViewPatch({
      hasCurrentSnapshot: true,
      currentEpoch: epochA,
      currentSeq: 18n,
      nextEpoch: epochA,
      nextSeq: 17n,
    }),
    { action: 'ignore' },
  )
})

test('resyncs on sequence gap', () => {
  assert.deepEqual(
    decideFarmViewPatch({
      hasCurrentSnapshot: true,
      currentEpoch: epochA,
      currentSeq: 17n,
      nextEpoch: epochA,
      nextSeq: 19n,
    }),
    { action: 'resync' },
  )
})

test('resyncs on activation epoch change', () => {
  assert.deepEqual(
    decideFarmViewPatch({
      hasCurrentSnapshot: true,
      currentEpoch: epochA,
      currentSeq: 17n,
      nextEpoch: epochB,
      nextSeq: 18n,
    }),
    { action: 'resync' },
  )
})
