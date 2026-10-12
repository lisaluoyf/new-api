import assert from 'node:assert/strict'
import { test } from 'node:test'
import { cacheWriteBasePrices } from './cache-write-base-prices'

test('Opus shows 5-minute and inherited 1-hour base prices without writing an override', () => {
  const model = {
    name: 'claude-opus-5-5',
    ratio: '2',
    createCacheRatio: '1.25',
  }
  assert.deepEqual(cacheWriteBasePrices(model), {
    fiveMinutes: 5,
    oneHour: 8,
    inheritsOneHour: true,
  })
  assert.equal('createCache1hRatio' in model, false)
})

test('explicit 1-hour prices and free cache writes take precedence over inheritance', () => {
  const model = {
    name: 'claude-sonnet-5-5',
    ratio: '1',
    createCacheRatio: '1.25',
  }
  assert.deepEqual(
    cacheWriteBasePrices({ ...model, createCache1hRatio: '3' }),
    { fiveMinutes: 2.5, oneHour: 6, inheritsOneHour: false }
  )
  assert.deepEqual(
    cacheWriteBasePrices({ ...model, createCache1hRatio: '0' }),
    { fiveMinutes: 2.5, oneHour: 0, inheritsOneHour: false }
  )
})

test('unknown base prices and non-Claude models do not invent a one-hour price', () => {
  assert.equal(
    cacheWriteBasePrices({ name: 'claude-opus-5-5' }).oneHour,
    undefined
  )
  assert.equal(
    cacheWriteBasePrices({
      name: 'gpt-6-astra',
      ratio: '5',
      createCacheRatio: '1.25',
    }).oneHour,
    undefined
  )
})
