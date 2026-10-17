import assert from 'node:assert/strict'
import { test } from 'node:test'
import { availableChannelCount, channelSelected } from './channel-selection'
import { providerSettingsSchema } from './schema'
import type { ProviderChannel } from '../api'

const channels: ProviderChannel[] = [
  { channel_id: 73, channel_category: 'official', status: 1 },
  { channel_id: 81, channel_category: 'official', status: 1 },
  { channel_id: 59, channel_category: 'cloud', status: 1 },
  { channel_id: 149, status: 1 },
  { channel_id: 102, channel_category: 'cloud', status: 2 },
].map((channel) => ({ user_price: null, actual_output_user_price: null, official_input_price: null, client_exclusive: '', ...channel }))

test('category coverage and explicit IDs form a union without duplicate candidates', () => {
  assert.equal(availableChannelCount(channels, [73, 59], ['official'], 'include'), 3)
  assert.equal(availableChannelCount(channels, [73, 59], ['official'], 'exclude'), 1)
  assert.equal(channelSelected(channels[0], [73], []), true)
  assert.equal(channelSelected(channels[1], [73], []), false)
  assert.equal(channelSelected(channels[3], [], ['default']), true)
  assert.equal(availableChannelCount(channels, [], ['cloud'], 'include'), 1)
  assert.equal(availableChannelCount(channels, [999], [], 'include'), 0)
})

test('category-only settings are valid and old ID-only rules remain compatible', () => {
  const rule = { enabled: true, model: 'gpt-image-2', mode: 'include', channel_ids: [] }
  assert.equal(providerSettingsSchema.safeParse({ rules: [{ ...rule, channel_categories: ['official'] }] }).success, true)
  assert.equal(providerSettingsSchema.safeParse({ rules: [{ ...rule, channel_ids: [73] }] }).success, true)
  assert.equal(providerSettingsSchema.safeParse({ rules: [rule] }).success, false)
  assert.equal(providerSettingsSchema.safeParse({ rules: [{ ...rule, channel_categories: ['unknown'] }] }).success, false)
})
