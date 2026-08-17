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
import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { DataTablePage } from '@/components/data-table'
import { SectionPageLayout } from '@/components/layout'
import { getBillingSummary } from './api'
import { buildBillingSummaryColumns } from './components/billing-summary-columns'
import { BillingSummaryFilterBar } from './components/billing-summary-filter-bar'
import { getDefaultBillingTimeRange } from './lib/utils'
import type { BillingSummaryFilters, BillingTableRow } from './types'

export function BillingSummaryPage() {
  const { t } = useTranslation()
  const [filters, setFilters] = useState<BillingSummaryFilters>(() => {
    const { start, end } = getDefaultBillingTimeRange()
    return { startTime: start, endTime: end }
  })

  const { data, isLoading, isFetching } = useQuery({
    queryKey: ['billing-summary', filters],
    queryFn: () => getBillingSummary(filters),
  })

  const rows = useMemo(() => (data?.success ? (data.data ?? []) : []), [data])
  const walletBalanceUSD =
    data?.success && data.wallet_balance_usd != null
      ? Number(data.wallet_balance_usd)
      : null
  const experienceBalanceUSD =
    data?.success && data.experience_balance_usd != null
      ? Number(data.experience_balance_usd)
      : null
  const paidSubscriptionBalanceUSD =
    data?.success && data.paid_subscription_balance_usd != null
      ? Number(data.paid_subscription_balance_usd)
      : null
  const nonSubscriptionUserCount =
    data?.success && data.non_subscription_user_count != null
      ? Number(data.non_subscription_user_count)
      : 0
  const experienceUserCount =
    data?.success && data.experience_user_count != null
      ? Number(data.experience_user_count)
      : 0

  // Prepend a synthetic "Total" row so the summed cost/revenue/profit/margin
  // render as a real, always-first table row instead of a separate element.
  const tableRows = useMemo<BillingTableRow[]>(() => {
    const totals = rows.reduce(
      (acc, row) => ({
        cost_usd: acc.cost_usd + row.cost_usd,
        revenue_usd: acc.revenue_usd + row.revenue_usd,
        experience_cost_usd:
          acc.experience_cost_usd + row.experience_cost_usd,
        experience_billing_usd:
          acc.experience_billing_usd + row.experience_billing_usd,
        paid_subscription_cost_usd:
          acc.paid_subscription_cost_usd + row.paid_subscription_cost_usd,
        paid_subscription_revenue_usd:
          acc.paid_subscription_revenue_usd + row.paid_subscription_revenue_usd,
        accounting_ok_request_count:
          acc.accounting_ok_request_count + row.accounting_ok_request_count,
        accounting_target_request_count:
          acc.accounting_target_request_count +
          row.accounting_target_request_count,
      }),
      {
        cost_usd: 0,
        revenue_usd: 0,
        experience_cost_usd: 0,
        experience_billing_usd: 0,
        paid_subscription_cost_usd: 0,
        paid_subscription_revenue_usd: 0,
        accounting_ok_request_count: 0,
        accounting_target_request_count: 0,
      }
    )
    return [
      {
        day: 0,
        ...totals,
        non_subscription_user_count: nonSubscriptionUserCount,
        experience_user_count: experienceUserCount,
        wallet_balance_usd: walletBalanceUSD,
        experience_balance_usd: experienceBalanceUSD,
        paid_subscription_balance_usd: paidSubscriptionBalanceUSD,
        isTotal: true,
      },
      ...rows,
    ]
  }, [
    rows,
    nonSubscriptionUserCount,
    experienceUserCount,
    walletBalanceUSD,
    experienceBalanceUSD,
    paidSubscriptionBalanceUSD,
  ])

  const columns = useMemo(() => buildBillingSummaryColumns(t), [t])

  const table = useReactTable({
    data: tableRows,
    columns,
    getCoreRowModel: getCoreRowModel(),
  })

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Platform Billing')}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t('Daily cost, revenue, profit and margin across the platform')}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        <DataTablePage
          table={table}
          columns={columns}
          applyHeaderSize
          isLoading={isLoading}
          isFetching={isFetching}
          hideMobile
          showPagination={false}
          tableClassName='[&_table]:text-xs [&_th]:h-9 [&_th]:overflow-hidden [&_th]:px-1.5 [&_th]:text-xs [&_td]:overflow-hidden [&_td]:px-1.5 [&_td]:py-2'
          emptyTitle={t('No Data')}
          getRowClassName={(row) =>
            row.original.isTotal ? 'bg-muted/40 border-b-2' : undefined
          }
          toolbar={
            <BillingSummaryFilterBar
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
