import { api } from '@/lib/api'
import type { DailyStatsFilters, DailyStatsResponse } from './types'

export async function getDailyStats(
  filters: DailyStatsFilters
): Promise<DailyStatsResponse> {
  const params = new URLSearchParams()
  if (filters.startTime) {
    params.set(
      'start_timestamp',
      String(Math.floor(filters.startTime.getTime() / 1000))
    )
  }
  if (filters.endTime) {
    params.set(
      'end_timestamp',
      String(Math.floor(filters.endTime.getTime() / 1000))
    )
  }

  const res = await api.get(`/api/daily-stats/?${params.toString()}`)
  const response = res.data as DailyStatsResponse
  if (!response.success) {
    throw new Error(response.message ?? 'Failed to load')
  }
  return response
}
