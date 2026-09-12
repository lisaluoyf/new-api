import assert from 'node:assert/strict'
import { test } from 'node:test'
import { sortChannelData } from './sort-channel-data'

const model = 'gpt-5.5'
const channels = [
  { id: 1, status: 1, model_enabled: true, user_price: 3 },
  { id: 2, status: 1, model_enabled: true, user_price: 1 },
  { id: 3, status: 1, model_enabled: false, user_price: 2 },
  { id: 4, status: 1, model_enabled: true, user_price: 2 },
]

test('initial load sorts enabled channels by user price without mutating input', () => {
  const before = structuredClone(channels)
  assert.deepEqual(
    sortChannelData(channels, model).map((row) => row.id),
    [2, 4, 1, 3]
  )
  assert.deepEqual(channels, before)
})

test('disabling the cheapest channel moves it to the disabled price group', () => {
  const response = channels.map((row) => ({
    ...row,
    model_enabled: row.id === 2 ? false : row.model_enabled,
  }))
  assert.deepEqual(
    sortChannelData(response, model).map((row) => row.id),
    [4, 1, 2, 3]
  )
})

test('enabling a channel inserts it by price and keeps equal prices stable', () => {
  const response = channels.map((row) => ({ ...row, model_enabled: true }))
  assert.deepEqual(
    sortChannelData(response, model).map((row) => row.id),
    [2, 3, 4, 1]
  )
})

test('channel status overrides model ability and missing prices stay last in each group', () => {
  const response = [
    { id: 1, status: 3, model_enabled: true, user_price: 0.1 },
    { id: 2, status: 1, model_enabled: true, user_price: null },
    { id: 3, status: 1, model_enabled: true, user_price: 2 },
    { id: 4, status: 2, model_enabled: true, user_price: null },
    { id: 5, status: 2, model_enabled: true, user_price: 0.2 },
    { id: 6, status: 1, model_enabled: true, user_price: 0 },
  ]
  assert.deepEqual(
    sortChannelData(response, model).map((row) => row.id),
    [3, 6, 2, 1, 5, 4]
  )
})

test('free model retains member enablement, priority and weight ordering', () => {
  const response = [
    { ...channels[0], free_model_config: { enabled: false, priority: 999 } },
    {
      ...channels[1],
      free_model_config: { enabled: true, priority: 100, weight: 1 },
    },
    {
      ...channels[2],
      free_model_config: { enabled: true, priority: 200, weight: 1 },
    },
    {
      ...channels[3],
      free_model_config: { enabled: true, priority: 100, weight: 2 },
    },
  ]
  assert.deepEqual(
    sortChannelData(response, 'apimaster-freemodel').map((row) => row.id),
    [3, 4, 2, 1]
  )
})
