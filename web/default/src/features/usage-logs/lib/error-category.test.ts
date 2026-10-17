import assert from 'node:assert/strict'
import { test } from 'node:test'
import { getErrorCategory } from './error-category'

test('classifies historical and structured errors without treating upstream quota as account quota', () => {
  assert.equal(
    getErrorCategory(
      'status_code=400, gpt-6.1-sol does not support reasoning effort "none"'
    ),
    'Request parameter error'
  )
  assert.equal(
    getErrorCategory('status_code=403, GPT Pass 的5 小时滚动额度已用尽'),
    'Account or plan quota limit'
  )
  assert.equal(
    getErrorCategory('status_code=403, 内容审计命中风险规则'),
    'Content policy rejection'
  )
  assert.equal(getErrorCategory('context canceled'), 'Client disconnected')
  assert.equal(
    getErrorCategory('status_code=524, upstream timeout'),
    'Upstream failure'
  )
  assert.equal(
    getErrorCategory('status_code=429, upstream quota exceeded'),
    'Upstream rate limit'
  )
  assert.equal(
    getErrorCategory('', { error_category: 'request_parameter' }),
    'Request parameter error'
  )
})
