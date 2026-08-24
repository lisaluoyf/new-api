/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { UsageLog } from '../data/schema'
import type { LogOtherData } from '../types'

export type DeepSeekV4PricePeriod = 'off_peak' | 'peak'

export interface DeepSeekV4TimedPricingDisplay {
  currentPeriod: DeepSeekV4PricePeriod
  currentInput: number
  currentOutput: number
}

const DEEPSEEK_V4_TIMED_PRICING_MODELS = new Set([
  'deepseek-v4-flash',
  'deepseek-v4-pro',
  'deepseek-v4-flash-vision-exp',
])

function isPeakAtUnixSeconds(timestamp: number): boolean {
  const beijingTime = new Date((timestamp + 8 * 60 * 60) * 1000)
  const weekday = beijingTime.getUTCDay()
  if (weekday === 0 || weekday === 6) return false
  const beijingHour = beijingTime.getUTCHours()
  return (
    (beijingHour >= 9 && beijingHour < 12) ||
    (beijingHour >= 14 && beijingHour < 18)
  )
}

export function getDeepSeekV4TimedPricingDisplay(
  log: UsageLog,
  other: LogOtherData
): DeepSeekV4TimedPricingDisplay | null {
  if (!DEEPSEEK_V4_TIMED_PRICING_MODELS.has(log.model_name)) {
    return null
  }

  const input =
    other.model_ratio != null && Number.isFinite(other.model_ratio)
      ? other.model_ratio * 2
      : other.ch_input_price
  const output =
    other.model_ratio != null &&
    Number.isFinite(other.model_ratio) &&
    other.completion_ratio != null &&
    Number.isFinite(other.completion_ratio)
      ? other.model_ratio * 2 * other.completion_ratio
      : other.ch_output_price
  if (
    input == null ||
    output == null ||
    !Number.isFinite(input) ||
    !Number.isFinite(output) ||
    input <= 0 ||
    output <= 0
  ) {
    return null
  }

  const currentPeriod: DeepSeekV4PricePeriod =
    other.ch_price_period ??
    (isPeakAtUnixSeconds(log.created_at) ? 'peak' : 'off_peak')

  return {
    currentPeriod,
    currentInput: input,
    currentOutput: output,
  }
}
