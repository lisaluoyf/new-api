import assert from 'node:assert/strict'
import test from 'node:test'
import type { User } from '../types'
import {
  transformFormDataToPayload,
  transformUserToFormDefaults,
  userFormSchema,
} from './user-form'

test('user default discount form reads the default and preserves model overrides', () => {
  const values = transformUserToFormDefaults({
    username: 'discount-owner',
    quota: 0,
    default_model_discount: 0.8,
    model_discount_ratios: { model: 1 },
  } as unknown as User)
  assert.equal(values.default_model_discount, 0.8)
  const payload = transformFormDataToPayload(values, 1)
  assert.equal(payload.default_model_discount, 0.8)
  assert.deepEqual(payload.model_discount_ratios, { model: 1 })
})

test('user default discount clears with null and keeps one as an explicit value', () => {
  assert.equal(
    transformFormDataToPayload({ username: 'owner' }, 1).default_model_discount,
    null
  )
  assert.equal(
    transformFormDataToPayload(
      { username: 'owner', default_model_discount: 1 },
      1
    ).default_model_discount,
    1
  )
  assert.equal(
    transformFormDataToPayload({
      username: 'owner',
      default_model_discount: 0.8,
    }).default_model_discount,
    undefined
  )
})

test('user default discount validates the same multiplier range as the backend', () => {
  for (const invalid of [0, -0.1, 1.01, 0.0000001, NaN, Infinity]) {
    assert.equal(
      userFormSchema.safeParse({
        username: 'owner',
        default_model_discount: invalid,
      }).success,
      false
    )
  }
  for (const valid of [undefined, 0.000001, 0.8, 1]) {
    assert.equal(
      userFormSchema.safeParse({
        username: 'owner',
        default_model_discount: valid,
      }).success,
      true
    )
  }
})
