import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { LogOtherData } from '../types'
import { getImageBillingBreakdown } from './image-billing'

function layerLog(): LogOtherData {
  return {
    model_price: 0.045,
    group_ratio: 1.05,
    user_group_ratio: -1,
    image_billing: {
      layer_decomposition: true,
      input_images: 1,
      generated_images: 4,
      base_amount_usd: 0.1125,
      base_prices: {
        'Image Output': 0.045,
        'Layer Image Output': 0.0225,
        'High-Resolution Layer Image Output': 0.045,
      },
      billable_counts: {
        'Layer Image Output': 3,
        'High-Resolution Layer Image Output': 1,
      },
    },
  }
}

test('explains a mixed-resolution layer bill from the recorded snapshot', () => {
  const result = getImageBillingBreakdown(layerLog())!
  assert.equal(result.outputImages, 4)
  assert.equal(result.freeInputImages, 1)
  assert.equal(result.paidInputImages, 0)
  assert.deepEqual(
    result.items.map(({ quantity, unitPrice }) => [quantity, unitPrice]),
    [
      [3, 0.0225],
      [1, 0.045],
    ]
  )
  assert.equal(result.channelRatio, 1)
  assert(Math.abs(result.calculatedCharge! - 0.118125) < 1e-10)
})

test('includes paid references and preserves channel and user multipliers', () => {
  const other = layerLog()
  other.model_price = 0.09
  other.user_group_ratio = 0.8
  other.image_billing = {
    layer_decomposition: false,
    input_images: 2,
    generated_images: 1,
    base_amount_usd: 0.048,
    base_prices: { 'Image Output': 0.045, 'Image Input (after first)': 0.003 },
    billable_counts: { 'Image Output': 1, 'Image Input (after first)': 1 },
  }
  const result = getImageBillingBreakdown(other)!
  assert.equal(result.paidInputImages, 1)
  assert.equal(result.channelRatio, 2)
  assert.equal(result.groupRatio, 0.8)
  assert(Math.abs(result.calculatedCharge! - 0.0768) < 1e-10)
})

test('does not invent image charges for unrelated or incomplete logs', () => {
  assert.equal(getImageBillingBreakdown(null), null)
  assert.equal(getImageBillingBreakdown({ model_price: 0.045 }), null)
  const other = layerLog()
  delete other.image_billing!.base_prices['Layer Image Output']
  assert.equal(getImageBillingBreakdown(other), null)
  const missingPrice = layerLog()
  delete missingPrice.model_price
  assert.equal(getImageBillingBreakdown(missingPrice)!.calculatedCharge, null)
})

test('Grok bills each reference once with the recorded variant and coefficients', () => {
  const result = getImageBillingBreakdown({
    model_price: 0.0768,
    group_ratio: 1.05,
    user_group_ratio: -1,
    image_billing: {
      base_variant: '2K medium',
      layer_decomposition: false,
      input_images: 1,
      generated_images: 2,
      base_amount_usd: 0.17,
      base_prices: { '2K medium': 0.08, 'Image Input': 0.01 },
      billable_counts: { '2K medium': 2, 'Image Input': 1 },
    },
  })!
  assert.equal(result.freeInputImages, 0)
  assert.equal(result.paidInputImages, 1)
  assert.equal(result.outputImages, 2)
  assert(Math.abs(result.channelRatio! - 0.96) < 1e-10)
  assert(Math.abs(result.calculatedCharge! - 0.17136) < 1e-10)
})
