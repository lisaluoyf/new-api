import dayjs from '@/lib/dayjs'
import type { DailyStatsRow, DailyStatsTableRow } from '../types'

export function getDefaultDailyStatsTimeRange(): { start: Date; end: Date } {
  const now = dayjs()
  return {
    start: now.subtract(6, 'day').startOf('day').toDate(),
    end: now.endOf('day').toDate(),
  }
}

export function buildDailyStatsTableRows(
  rows: DailyStatsRow[]
): DailyStatsTableRow[] {
  const total = rows.reduce<DailyStatsRow>(
    (sum, row) => ({
      day: 0,
      uv: sum.uv + row.uv,
      pv: sum.pv + row.pv,
      traffic_available: sum.traffic_available || row.traffic_available,
      registration_count: sum.registration_count + row.registration_count,
      telegram_registration_count:
        sum.telegram_registration_count + row.telegram_registration_count,
      google_registration_count:
        sum.google_registration_count + row.google_registration_count,
      referral_registration_count:
        sum.referral_registration_count + row.referral_registration_count,
      paying_user_count: sum.paying_user_count + row.paying_user_count,
      same_day_paying_registration_count:
        sum.same_day_paying_registration_count +
        row.same_day_paying_registration_count,
      paid_amount_usd: sum.paid_amount_usd + row.paid_amount_usd,
      commission_usd: sum.commission_usd + row.commission_usd,
      first_purchase_count:
        sum.first_purchase_count + row.first_purchase_count,
      trial_claim_count: sum.trial_claim_count + row.trial_claim_count,
      trial_rejected_count:
        sum.trial_rejected_count + row.trial_rejected_count,
      benefit_impression_count:
        sum.benefit_impression_count + row.benefit_impression_count,
      benefit_click_count:
        sum.benefit_click_count + row.benefit_click_count,
      onboarding_impression_count:
        sum.onboarding_impression_count + row.onboarding_impression_count,
      key_user_count: sum.key_user_count + row.key_user_count,
      home_click_count: sum.home_click_count + row.home_click_count,
      updated_at: Math.max(sum.updated_at, row.updated_at),
    }),
    {
      day: 0,
      uv: 0,
      pv: 0,
      traffic_available: false,
      registration_count: 0,
      telegram_registration_count: 0,
      google_registration_count: 0,
      referral_registration_count: 0,
      paying_user_count: 0,
      same_day_paying_registration_count: 0,
      paid_amount_usd: 0,
      commission_usd: 0,
      first_purchase_count: 0,
      trial_claim_count: 0,
      trial_rejected_count: 0,
      benefit_impression_count: 0,
      benefit_click_count: 0,
      onboarding_impression_count: 0,
      key_user_count: 0,
      home_click_count: 0,
      updated_at: 0,
    }
  )
  return [{ ...total, isTotal: true }, ...rows]
}
