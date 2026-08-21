import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import { isLogMediaVideoModel } from './media-preview'

describe('isLogMediaVideoModel', () => {
  test('recognizes Kling V3 Omni usage logs as video', () => {
    assert.equal(isLogMediaVideoModel('kling-v3-omni'), true)
    assert.equal(isLogMediaVideoModel(' KLING-V3-OMNI '), true)
  })
})
