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

  test('recognizes both Seedance models and builds authenticated previews', () => {
    assert.equal(isLogMediaVideoModel('doubao-seedance-2.0'), true)
    assert.equal(isLogMediaVideoModel('SEEDANCE-2.5'), true)

    for (const model of ['doubao-seedance-2.0', 'seedance-2.5']) {
      const preview = getLogMediaPreview(
        { type: 2, model_name: model } as never,
        {
          task_id: 'task_seedance_success',
          result_url: '/v1/videos/task_seedance_success/content',
        }
      )
      assert.deepEqual(preview, {
        kind: 'video',
        url: '/v1/videos/task_seedance_success/content',
        taskId: 'task_seedance_success',
      })
    }
  })
})
