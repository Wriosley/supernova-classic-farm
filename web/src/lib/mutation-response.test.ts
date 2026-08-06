import assert from 'node:assert/strict'
import test from 'node:test'
import { create } from '@bufbuild/protobuf'
import {
  BuyFertilizerResponseSchema,
  BuyFertilizerRequestSchema,
  PlayerStatePatchSchema,
  WsEnvelopeSchema,
} from '../gen/classicfarm/v1/ws/ws_pb'
import { mutationResponsePatch } from './mutation-response'

test('extracts the patch from a fertilizer purchase response', () => {
  const patch = create(PlayerStatePatchSchema, {
    coinBalance: 98n,
    inventoryUpserts: [{ itemId: 1, quantity: 2 }],
  })
  const response = create(WsEnvelopeSchema, {
    payload: {
      case: 'buyFertilizerResponse',
      value: create(BuyFertilizerResponseSchema, { patch }),
    },
  })

  assert.deepEqual(mutationResponsePatch(response), patch)
})

test('does not treat a mutation request as a response patch', () => {
  const request = create(WsEnvelopeSchema, {
    payload: {
      case: 'buyFertilizerRequest',
      value: create(BuyFertilizerRequestSchema),
    },
  })

  assert.equal(mutationResponsePatch(request), undefined)
})
