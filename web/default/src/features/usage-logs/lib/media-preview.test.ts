import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  getLogMediaPreview,
  isLogMediaImageModel,
  isLogMediaVideoModel,
} from './media-preview'

describe('isLogMediaImageModel', () => {
  test('recognizes both Midjourney models and preserves all unique result images', () => {
    for (const model of ['midjourney-v8.2', 'midjourney-niji-7']) {
      assert.equal(isLogMediaImageModel(model.toUpperCase()), true)
      const urls = [1, 2, 3, 4].map((n) => `https://apimaster.ai/imgs/${n}.png`)
      assert.deepEqual(
        getLogMediaPreview({ type: 2, model_name: model } as never, {
          result_url: urls[0],
          result_urls: [...urls, urls[0]],
          task_id: 'imagine_test',
        }),
        { kind: 'image', url: urls[0], urls, taskId: 'imagine_test' }
      )
    }
  })

  test('uses the result array when the primary URL is absent or invalid', () => {
    assert.deepEqual(
      getLogMediaPreview({ type: 2, model_name: 'midjourney-v8.2' } as never, {
        result_url: 'failed',
        result_urls: [' ', 'javascript:invalid', ' /imgs/valid.png '],
      }),
      { kind: 'image', url: '/imgs/valid.png', taskId: undefined }
    )
  })

  test('does not preview a refund or an unrelated text model', () => {
    for (const log of [
      { type: 6, model_name: 'midjourney-v8.2' },
      { type: 2, model_name: 'gpt-5.5' },
    ]) {
      assert.equal(
        getLogMediaPreview(log as never, { result_urls: ['/imgs/1.png'] }),
        null
      )
    }
  })

  test('preserves the GPT Image single-image preview', () => {
    assert.deepEqual(
      getLogMediaPreview({ type: 2, model_name: 'gpt-image-2' } as never, {
        result_url: '/imgs/gpt.png',
      }),
      { kind: 'image', url: '/imgs/gpt.png', taskId: undefined }
    )
  })
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
