import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import type { UsageLog } from '../data/schema'
import type { LogOtherData } from '../types'
import { getDeepSeekV4TimedPricingDisplay } from './deepseek-v4-pricing'

function usageLog(modelName: string, isoTime: string): UsageLog {
  return {
    model_name: modelName,
    created_at: Date.parse(isoTime) / 1000,
  } as UsageLog
}

describe('getDeepSeekV4TimedPricingDisplay', () => {
  test('formats DeepSeek V4 Vision with the archived peak period', () => {
    const display = getDeepSeekV4TimedPricingDisplay(
      usageLog('deepseek-v4-flash-vision-exp', '2026-08-24T02:08:01Z'),
      {
        model_ratio: 0.2178,
        completion_ratio: 3,
        ch_price_period: 'peak',
      } as LogOtherData
    )

    assert.deepEqual(display, {
      currentPeriod: 'peak',
      currentInput: 0.4356,
      currentOutput: 1.3068,
    })
  })

  test('treats weekend peak hours as off-peak when old logs lack a period', () => {
    const display = getDeepSeekV4TimedPricingDisplay(
      usageLog('deepseek-v4-flash', '2026-08-22T02:00:00Z'),
      { model_ratio: 0.11, completion_ratio: 3 } as LogOtherData
    )

    assert.equal(display?.currentPeriod, 'off_peak')
  })

  test('rejects models outside DeepSeek V4 timed pricing', () => {
    const display = getDeepSeekV4TimedPricingDisplay(
      usageLog('deepseek-chat', '2026-08-24T02:08:01Z'),
      { model_ratio: 0.11, completion_ratio: 3 } as LogOtherData
    )

    assert.equal(display, null)
  })
})
