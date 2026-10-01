import { api } from '@/lib/api'

export type ReconciliationFilters = {
  start_date: string
  end_date: string
  provider: string
}
export type ReconciliationTotal = {
  currency: string
  local_amount: string
  official_amount: string
  difference: string
}
export type ReconciliationRun = {
  id: number
  day: string
  provider: string
  status: string
  started_at: number
  finished_at: number
  checked_count: number
  local_paid_count: number
  official_paid_count: number
  difference_count: number
  unverified_count: number
  totals: ReconciliationTotal[]
}
export type ReconciliationJob = {
  id: number
  day: string
  provider: string
  status: string
  attempts: number
  next_attempt: number
  run_id: number
}
export type ReconciliationItem = {
  id: number
  run_id: number
  trade_no: string
  user_id: number
  purpose: string
  local_status: string
  local_amount: string
  currency: string
  official_id: string
  official_status: string
  official_amount: string
  official_currency: string
  result: string
  problem: string
  checked_at: number
}
export type ReconciliationResponse = {
  success: boolean
  message?: string
  jobs: ReconciliationJob[]
  runs: ReconciliationRun[]
  items: ReconciliationItem[]
  items_total: number
  page: number
  items_truncated: boolean
}
export const providers = [
  'epay',
  'platega',
  'stripe',
  'paypal',
  'creem',
  'clink',
  'waffo',
  'waffo_pancake',
  'nowpayments',
  'crypto',
  'unknown',
]
export function yesterdayBeijing(): string {
  return new Date(Date.now() + 8 * 3600_000 - 86400_000)
    .toISOString()
    .slice(0, 10)
}
export async function getReconciliation(
  filters: ReconciliationFilters,
  page = 1
): Promise<ReconciliationResponse> {
  const response = await api.get('/api/payment-reconciliation/', {
    params: { ...filters, page },
  })
  const data = response.data as ReconciliationResponse
  if (!data.success) throw new Error(data.message ?? 'Failed to load')
  return data
}
export async function queueReconciliation(
  filters: ReconciliationFilters
): Promise<void> {
  const response = await api.post('/api/payment-reconciliation/run', filters)
  if (!response.data.success)
    throw new Error(response.data.message ?? 'Failed to load')
}
