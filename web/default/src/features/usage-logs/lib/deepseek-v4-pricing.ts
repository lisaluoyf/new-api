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
  offPeakInput: number
  offPeakOutput: number
  peakInput: number
  peakOutput: number
  groupRatio: number
  finalInput: number
  finalOutput: number
}

function isPeakAtUnixSeconds(timestamp: number): boolean {
  const beijingHour = new Date((timestamp + 8 * 60 * 60) * 1000).getUTCHours()
  return (
    (beijingHour >= 9 && beijingHour < 12) ||
    (beijingHour >= 14 && beijingHour < 18)
  )
}

function effectiveGroupRatio(other: LogOtherData): number {
  const userRatio = other.user_group_ratio
  if (userRatio != null && Number.isFinite(userRatio) && userRatio !== -1) {
    return userRatio
  }
  if (other.group_ratio != null && Number.isFinite(other.group_ratio)) {
    return other.group_ratio
  }
  return 1
}

export function getDeepSeekV4TimedPricingDisplay(
  log: UsageLog,
  other: LogOtherData
): DeepSeekV4TimedPricingDisplay | null {
  if (
    log.model_name !== 'deepseek-v4-flash' &&
    log.model_name !== 'deepseek-v4-pro'
  ) {
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
  const offPeakInput = currentPeriod === 'peak' ? input / 2 : input
  const offPeakOutput = currentPeriod === 'peak' ? output / 2 : output
  const peakInput = offPeakInput * 2
  const peakOutput = offPeakOutput * 2
  const groupRatio = effectiveGroupRatio(other)

  return {
    currentPeriod,
    offPeakInput,
    offPeakOutput,
    peakInput,
    peakOutput,
    groupRatio,
    finalInput: input * groupRatio,
    finalOutput: output * groupRatio,
  }
}
