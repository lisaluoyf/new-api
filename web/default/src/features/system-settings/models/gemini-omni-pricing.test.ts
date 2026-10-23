import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  OMNI_MODEL,
  OMNI_TIERS,
  mergeOmniBasePrice,
  parseOmniPrices,
} from './gemini-omni-pricing.ts'

test('all 24 tariffs required and invalid prices rejected', () => {
  const prices = Object.fromEntries(OMNI_TIERS.map((key) => [key, 0.05]))
  assert.equal(Object.keys(parseOmniPrices(JSON.stringify(prices))).length, 24)
  assert.throws(() => parseOmniPrices('{}'))
  assert.throws(() =>
    parseOmniPrices(JSON.stringify({ ...prices, 'Official-720P': 0 }))
  )
  assert.throws(() =>
    parseOmniPrices(JSON.stringify({ ...prices, 'Official-720P': '0.05' }))
  )
})
test('base save preserves other models, variants and official benchmark', () => {
  const merged = mergeOmniBasePrice(
    '{"other":2}',
    JSON.stringify({
      other: { prices: { '4K': 3 } },
      [OMNI_MODEL]: {
        prices: { '720P': 0.1 },
        official_prices: { '720P': 0.10136 },
      },
    }),
    0.2
  )
  assert.deepEqual(JSON.parse(merged.ModelPrice), {
    other: 2,
    [OMNI_MODEL]: 0.2,
  })
  const video = JSON.parse(merged.VideoModelPricing)
  assert.equal(video[OMNI_MODEL].base_price, 0.2)
  assert.equal(video[OMNI_MODEL].prices['720P'], 0.2)
  assert.equal(video[OMNI_MODEL].official_prices['720P'], 0.10136)
  assert.deepEqual(video.other, { prices: { '4K': 3 } })
  assert.throws(() => mergeOmniBasePrice('{}', '{}', 0))
  assert.throws(() => mergeOmniBasePrice('[]', '{}', 1))
})
