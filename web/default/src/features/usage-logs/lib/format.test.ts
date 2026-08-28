/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import { getCodingPlanEffectivePrices } from './format'

describe('getCodingPlanEffectivePrices', () => {
  test('uses archived official prices multiplied by the request multiplier', () => {
    const prices = getCodingPlanEffectivePrices({
      subscription_type: 'coding_plan',
      model_ratio: 0,
      completion_ratio: 0,
      coding_plan_multiplier: 0.03,
      coding_official_input_price: 5,
      coding_official_output_price: 30,
      coding_official_cache_read_price: 0.5,
      coding_official_cache_write_price: 5,
    })

    assert.ok(prices)
    assert.ok(Math.abs(prices.input - 0.15) < 1e-12)
    assert.ok(Math.abs(prices.output - 0.9) < 1e-12)
    assert.ok(
      prices.cacheRead != null && Math.abs(prices.cacheRead - 0.015) < 1e-12
    )
    assert.ok(
      prices.cacheWrite != null && Math.abs(prices.cacheWrite - 0.15) < 1e-12
    )
  })

  test('does not guess prices when archived Coding Plan fields are incomplete', () => {
    assert.equal(
      getCodingPlanEffectivePrices({
        subscription_type: 'coding_plan',
        coding_plan_multiplier: 0.03,
        coding_official_input_price: 5,
      }),
      null
    )
  })
})
