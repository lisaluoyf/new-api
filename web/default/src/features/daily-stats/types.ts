export interface DailyStatsFilters {
  startTime?: Date
  endTime?: Date
}

export interface DailyStatsRow {
  day: number
  uv: number
  pv: number
  traffic_available: boolean
  registration_count: number
  telegram_registration_count: number
  google_registration_count: number
  referral_registration_count: number
  paying_user_count: number
  same_day_paying_registration_count: number
  paid_amount_usd: number
  commission_usd: number
  first_purchase_count: number
  trial_claim_count: number
  trial_rejected_count: number
  benefit_impression_count: number
  benefit_click_count: number
  onboarding_impression_count: number
  key_user_count: number
  home_click_count: number
  updated_at: number
}

export interface DailyStatsTableRow extends DailyStatsRow {
  isTotal?: boolean
}

export interface DailyStatsResponse {
  success: boolean
  message?: string
  data?: DailyStatsRow[]
  updated_at?: number
}
