import { useState } from 'react'
import { type Table } from '@tanstack/react-table'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import { DataTableToolbar } from '@/components/data-table'
import { getDefaultDailyStatsTimeRange } from '../lib/utils'
import type { DailyStatsFilters } from '../types'

interface DailyStatsFilterBarProps<TData> {
  table: Table<TData>
  onApply: (filters: DailyStatsFilters) => void
  isFetching?: boolean
}

export function DailyStatsFilterBar<TData>(
  props: DailyStatsFilterBarProps<TData>
) {
  const [filters, setFilters] = useState<DailyStatsFilters>(() => {
    const range = getDefaultDailyStatsTimeRange()
    return { startTime: range.start, endTime: range.end }
  })

  const handleReset = () => {
    const range = getDefaultDailyStatsTimeRange()
    const resetFilters = { startTime: range.start, endTime: range.end }
    setFilters(resetFilters)
    props.onApply(resetFilters)
  }

  return (
    <DataTableToolbar
      table={props.table}
      customSearch={
        <CompactDateTimeRangePicker
          start={filters.startTime}
          end={filters.endTime}
          onChange={({ start, end }) =>
            setFilters({ startTime: start, endTime: end })
          }
          className='w-full sm:w-[340px]'
        />
      }
      onSearch={() => props.onApply(filters)}
      searchLoading={props.isFetching}
      onReset={handleReset}
      hideViewOptions
    />
  )
}
