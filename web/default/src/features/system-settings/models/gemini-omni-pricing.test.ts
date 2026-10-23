import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  OMNI_MODEL,
  OMNI_RESOLUTIONS,
  OMNI_TOKENS_PER_SECOND,
  OMNI_OFFICIAL_PRICES,
  readOmniPrices,
  mergeOmniPrices,
} from './gemini-omni-pricing.ts'

test('all four list prices derive from Google output token rates', () => {
  for (const key of OMNI_RESOLUTIONS)
    assert.ok(
      Math.abs(
        OMNI_OFFICIAL_PRICES[key] - (17.5 * OMNI_TOKENS_PER_SECOND[key]) / 1e6
      ) < 1e-12
    )
  const old = JSON.stringify({ [OMNI_MODEL]: { prices: { '720P': 0.10136 } } })
  assert.deepEqual(
    readOmniPrices(old),
    Object.fromEntries(
      Object.entries(OMNI_OFFICIAL_PRICES).map(([key, value]) => [
        key,
        String(value),
      ])
    )
  )
})
test('official save synchronizes base and all tiers and preserves unrelated models', () => {
  const values = readOmniPrices('{}')
  const result = mergeOmniPrices(
    '{"other":2}',
    JSON.stringify({
      other: { prices: { '4K': 3 } },
      [OMNI_MODEL]: { unit: 'second', prices: { '720P': 0.10136 } },
    }),
    values
  )
  assert.deepEqual(JSON.parse(result.ModelPrice), {
    other: 2,
    [OMNI_MODEL]: 0.10136,
  })
  const video = JSON.parse(result.VideoModelPricing)
  assert.equal(video[OMNI_MODEL].base_price, 0.10136)
  assert.deepEqual(video[OMNI_MODEL].prices, OMNI_OFFICIAL_PRICES)
  assert.deepEqual(video[OMNI_MODEL].official_prices, OMNI_OFFICIAL_PRICES)
  assert.deepEqual(video.other, { prices: { '4K': 3 } })
  assert.throws(() => mergeOmniPrices('{}', '{}', { ...values, '4K': '0' }))
  assert.throws(() => mergeOmniPrices('{}', '[]', values))
})
