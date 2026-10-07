import assert from 'node:assert/strict'
import { test } from 'node:test'
import { MODEL_TABS } from './constants'
import {
  MODEL_CATEGORY_FILTERS,
  getPriceUnit,
  isLLMModel,
  modelMatchesCategory,
} from './model-classification'

test('Gemini has a dedicated category between Claude and Grok', () => {
  const categoryKeys = MODEL_CATEGORY_FILTERS.map((category) => category.key)
  const geminiIndex = categoryKeys.indexOf('gemini')
  assert.ok(geminiIndex >= 0)
  assert.equal(categoryKeys[geminiIndex - 1], 'claude')
  assert.equal(categoryKeys[geminiIndex + 1], 'grok')
  assert.equal(MODEL_CATEGORY_FILTERS[geminiIndex].label, 'Gemini')
  assert.equal(new Set(categoryKeys).size, categoryKeys.length)
})

test('Gemini category includes its text and Nano Banana image models only', () => {
  const models = MODEL_TABS.filter((tab) =>
    modelMatchesCategory(tab.modelId, 'gemini')
  ).map((tab) => tab.modelId)
  assert.deepEqual(
    models,
    MODEL_TABS.filter((tab) => tab.modelId.startsWith('gemini-')).map(
      (tab) => tab.modelId
    )
  )
  for (const modelId of [
    'gemini-3.1-pro-preview',
    'gemini-3.7-flash',
    'gemini-2.5-flash-image',
    'gemini-3-pro-image',
    'gemini-3.1-flash-image',
    'gemini-nano-banana-2.1',
  ]) {
    assert.ok(models.includes(modelId), modelId)
    assert.ok(modelMatchesCategory(modelId, 'foreign'), modelId)
    assert.equal(modelMatchesCategory(modelId, 'domestic'), false, modelId)
  }
  for (const modelId of ['gpt-5.5', 'claude-opus-5', 'grok-4.5']) {
    assert.equal(modelMatchesCategory(modelId, 'gemini'), false, modelId)
  }
})

test('Nano Banana 2.1 is registered once after Nano Banana 2', () => {
  const matches = MODEL_TABS.filter(
    (tab) => tab.modelId === 'gemini-nano-banana-2.1'
  )
  assert.equal(matches.length, 1)
  assert.equal(matches[0].label, 'Nano Banana 2.1')
  assert.equal(matches[0].accent, '#4285f4')
  const modelIndex = MODEL_TABS.findIndex(
    (tab) => tab.modelId === 'gemini-nano-banana-2.1'
  )
  assert.equal(MODEL_TABS[modelIndex - 1].modelId, 'gemini-3.1-flash-image')
  assert.ok(modelMatchesCategory(matches[0].modelId, 'all'))
})

test('Nano Banana models use per-image units without LLM token price columns', () => {
  for (const modelId of [
    'gemini-2.5-flash-image',
    'gemini-3-pro-image',
    'gemini-3.1-flash-image',
    'gemini-3.1-flash-image-preview',
    'gemini-nano-banana-2.1',
  ]) {
    assert.equal(isLLMModel(modelId), false, modelId)
    assert.equal(getPriceUnit(modelId), '$/req', modelId)
  }
})

test('existing model families and price units remain unchanged', () => {
  assert.ok(modelMatchesCategory('gpt-5.5', 'gpt'))
  assert.ok(modelMatchesCategory('claude-opus-5', 'claude'))
  assert.ok(modelMatchesCategory('seedance-2.5', 'doubao'))
  assert.ok(modelMatchesCategory('seedance-2.5', 'domestic'))
  assert.ok(modelMatchesCategory('text-embedding-3-small', 'embeddings'))
  assert.ok(modelMatchesCategory('bge-m3', 'embeddings'))
  assert.equal(isLLMModel('gemini-3.1-pro-preview'), true)
  assert.equal(getPriceUnit('gemini-3.1-pro-preview'), '$/1M')
  assert.equal(isLLMModel('apimaster-freemodel'), false)
  assert.equal(getPriceUnit('seedance-2.5'), '$/s')
  assert.equal(getPriceUnit('midjourney-v8.2'), '$/generation')
})
