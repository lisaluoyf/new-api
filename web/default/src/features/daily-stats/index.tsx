import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import dayjs from '@/lib/dayjs'
import { DataTablePage } from '@/components/data-table'
import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertAction, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { getDailyStats } from './api'
import { buildDailyStatsColumns } from './components/daily-stats-columns'
import { DailyStatsFilterBar } from './components/daily-stats-filter-bar'
import {
  buildDailyStatsTableRows,
  getDefaultDailyStatsTimeRange,
} from './lib/utils'
import type { DailyStatsFilters } from './types'

export function DailyStatsPage() {
  const { t } = useTranslation()
  const [filters, setFilters] = useState<DailyStatsFilters>(() => {
    const range = getDefaultDailyStatsTimeRange()
    return { startTime: range.start, endTime: range.end }
  })
  const { data, isLoading, isFetching, isError, refetch } = useQuery({
    queryKey: ['daily-stats', filters],
    queryFn: () => getDailyStats(filters),
    staleTime: 60_000,
    refetchInterval: 300_000,
    placeholderData: (previousData) => previousData,
    refetchOnWindowFocus: false,
  })

  const rows = useMemo(
    () => buildDailyStatsTableRows(data?.success ? (data.data ?? []) : []),
    [data]
  )
  const columns = useMemo(() => buildDailyStatsColumns(t), [t])
  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
  })
  const updatedAt = data?.updated_at
    ? dayjs(data.updated_at * 1000).format('YYYY-MM-DD HH:mm:ss')
    : '—'

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Daily Stats')}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t('Last updated:')} {updatedAt} · UTC+8
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        {isError && (
          <Alert variant='destructive' className='mb-3'>
            <AlertDescription>{t('Failed to load')}</AlertDescription>
            <AlertAction>
              <Button
                size='sm'
                variant='outline'
                onClick={() => void refetch()}
              >
                {t('Try again')}
              </Button>
            </AlertAction>
          </Alert>
        )}
        <DataTablePage
          table={table}
          columns={columns}
          applyHeaderSize
          isLoading={isLoading}
          isFetching={isFetching}
          hideMobile
          showPagination={false}
          tableClassName='[&_table]:min-w-[1960px] [&_table]:text-xs [&_th]:h-9 [&_th]:overflow-hidden [&_th]:px-1 [&_th]:text-xs [&_th]:whitespace-nowrap [&_td]:overflow-hidden [&_td]:px-1 [&_td]:py-2'
          emptyTitle={isError ? t('Failed to load') : t('No Data')}
          getRowClassName={(row) =>
            row.original.isTotal ? 'bg-muted/40 border-b-2' : undefined
          }
          toolbar={
            <DailyStatsFilterBar
              table={table}
              isFetching={isFetching}
              onApply={setFilters}
            />
          }
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
