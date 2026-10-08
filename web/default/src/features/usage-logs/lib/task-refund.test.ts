import assert from 'node:assert/strict'
import { test } from 'node:test'
import { isRefundedConsumeLog } from './task-refund'

test('a confirmed refund clears the charge only on the original consume row', () => {
  assert.equal(
    isRefundedConsumeLog({ type: 2 }, { billing_refunded: true }),
    true
  )
  assert.equal(
    isRefundedConsumeLog({ type: 6 }, { billing_refunded: true }),
    false
  )
  assert.equal(
    isRefundedConsumeLog({ type: 2 }, { billing_refunded: false }),
    false
  )
  assert.equal(isRefundedConsumeLog({ type: 2 }, null), false)
})

test('a failure or pending refund must not appear as an already refunded charge', () => {
  for (const other of [
    { task_fail_reason: 'content safety review failed' },
    { task_fail_code: 'task_failed' },
    { reason: 'refund pending' },
  ]) {
    assert.equal(isRefundedConsumeLog({ type: 2 }, other), false)
  }
})
