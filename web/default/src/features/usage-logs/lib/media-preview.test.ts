import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  getLogMediaPreview,
  isLogMediaImageModel,
  isLogMediaVideoModel,
} from './media-preview'

describe('isLogMediaImageModel', () => {
  test('recognizes all supported Gemini image model families', () => {
    assert.equal(isLogMediaImageModel('gemini-2.5-flash-image'), true)
    assert.equal(isLogMediaImageModel('gemini-3.1-flash-image'), true)
    assert.equal(isLogMediaImageModel('gemini-3-pro-image-preview'), true)
  })

  test('builds a preview from a Gemini image consume log', () => {
    const preview = getLogMediaPreview(
      {
        type: 2,
        model_name: 'gemini-2.5-flash-image',
      } as never,
      { result_url: 'https://apimaster.ai/imgs/result.png' }
    )

    assert.deepEqual(preview, {
      kind: 'image',
      url: 'https://apimaster.ai/imgs/result.png',
      taskId: undefined,
    })
  })
})

describe('isLogMediaVideoModel', () => {
  test('recognizes Kling V3 Omni usage logs as video', () => {
    assert.equal(isLogMediaVideoModel('kling-v3-omni'), true)
    assert.equal(isLogMediaVideoModel(' KLING-V3-OMNI '), true)
  })
})
