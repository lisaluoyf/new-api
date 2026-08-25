import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import { isOAuthBindSuccessResponse } from './oauth-response'

describe('isOAuthBindSuccessResponse', () => {
  test('recognizes the current API response shape', () => {
    assert.equal(
      isOAuthBindSuccessResponse({
        success: true,
        message: 'Binding successful',
        data: { action: 'bind' },
      }),
      true
    )
  })

  test('keeps compatibility with the legacy message marker', () => {
    assert.equal(
      isOAuthBindSuccessResponse({
        success: true,
        message: 'bind',
      }),
      true
    )
  })

  test('does not classify login or failed responses as binding success', () => {
    assert.equal(
      isOAuthBindSuccessResponse({ success: true, data: { id: 1 } }),
      false
    )
    assert.equal(
      isOAuthBindSuccessResponse({
        success: false,
        data: { action: 'bind' },
      }),
      false
    )
  })
})
