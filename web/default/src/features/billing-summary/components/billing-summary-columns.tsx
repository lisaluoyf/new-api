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
import { type ColumnDef } from '@tanstack/react-table'
import { DataTableColumnHeader } from '@/components/data-table'
import { formatDay, formatUSD } from '../constants'
import type { BillingTableRow } from '../types'

// Profit and margin are derived here from cost/revenue — not sent by the
// backend, so they never go stale relative to the source numbers.
// 毛利 (margin) is intentionally profit/cost, not profit/revenue.
export function buildBillingSummaryColumns(
  t: (key: string) => string
): ColumnDef<BillingTableRow, unknown>[] {
  const compactHeaderClass =
    'space-x-0 [&_button]:-ms-1! [&_button]:h-7! [&_button]:px-1! [&_svg]:ms-1! [&_svg]:size-3!'
  const getNonSubscriptionCost = (row: BillingTableRow) =>
    row.cost_usd - row.experience_cost_usd
  const getNonSubscriptionRevenue = (row: BillingTableRow) =>
    row.revenue_usd - row.experience_billing_usd

  return [
    {
      accessorKey: 'day',
      size: 90,
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('Date')}
          className={compactHeaderClass}
        />
      ),
      cell: ({ row }) =>
        row.original.isTotal ? (
          <span className='text-xs font-semibold'>{t('Total')}</span>
        ) : (
          <span className='font-mono text-xs'>
            {formatDay(row.original.day)}
          </span>
        ),
    },
    {
      id: 'accounting_requests',
      size: 170,
      header: () => <span>{t('Accounting OK / Requests (%)')}</span>,
      cell: ({ row }) => {
        const okCount = row.original.accounting_ok_request_count ?? 0
        const targetCount = row.original.accounting_target_request_count ?? 0
        const ratio = targetCount > 0 ? (okCount / targetCount) * 100 : null
        return (
          <span className='text-xs whitespace-nowrap tabular-nums'>
            {okCount}/{targetCount}
            <span className='text-muted-foreground text-xs'>
              {ratio == null ? '' : ` (${ratio.toFixed(1)}%)`}
            </span>
          </span>
        )
      },
    },
    {
      accessorKey: 'non_subscription_user_count',
      size: 50,
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('Users')}
          className={compactHeaderClass}
        />
      ),
      cell: ({ row }) => (
        <span
          className={`text-xs tabular-nums ${row.original.isTotal ? 'font-semibold' : ''}`}
        >
          {row.original.non_subscription_user_count ?? 0}
        </span>
      ),
    },
    {
      accessorKey: 'cost_usd',
      size: 56,
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('Platform Cost')}
          className={compactHeaderClass}
        />
      ),
      cell: ({ row }) => (
        <span className='text-xs tabular-nums'>
          {formatUSD(getNonSubscriptionCost(row.original))}
        </span>
      ),
    },
    {
      accessorKey: 'revenue_usd',
      size: 56,
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('Revenue')}
          className={compactHeaderClass}
        />
      ),
      cell: ({ row }) => (
        <span className='text-xs tabular-nums'>
          {formatUSD(getNonSubscriptionRevenue(row.original))}
        </span>
      ),
    },
    {
      id: 'profit_usd',
      size: 54,
      header: () => <span>{t('Profit')}</span>,
      cell: ({ row }) => {
        const profit =
          getNonSubscriptionRevenue(row.original) -
          getNonSubscriptionCost(row.original)
        return (
          <span
            className={`text-xs tabular-nums ${profit < 0 ? 'text-destructive' : ''}`}
          >
            {formatUSD(profit)}
          </span>
        )
      },
    },
    {
      id: 'margin',
      size: 56,
      header: () => <span>{t('Margin')}</span>,
      cell: ({ row }) => {
        const cost = getNonSubscriptionCost(row.original)
        const revenue = getNonSubscriptionRevenue(row.original)
        if (cost <= 0)
          return <span className='text-muted-foreground text-xs'>—</span>
        const margin = ((revenue - cost) / cost) * 100
        return (
          <span
            className={`text-xs tabular-nums ${margin < 0 ? 'text-destructive' : ''}`}
          >
            {margin.toFixed(1)}%
          </span>
        )
      },
    },
    {
      accessorKey: 'experience_user_count',
      size: 74,
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('Experience Users')}
          className={compactHeaderClass}
        />
      ),
      cell: ({ row }) => (
        <span
          className={`text-xs tabular-nums ${row.original.isTotal ? 'font-semibold' : ''}`}
        >
          {row.original.experience_user_count ?? 0}
        </span>
      ),
    },
    {
      accessorKey: 'experience_cost_usd',
      size: 74,
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('Experience Cost')}
          className={compactHeaderClass}
        />
      ),
      cell: ({ row }) => (
        <span className='text-xs tabular-nums'>
          {formatUSD(row.original.experience_cost_usd)}
        </span>
      ),
    },
    {
      accessorKey: 'experience_billing_usd',
      size: 76,
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('Experience Billing')}
          className={compactHeaderClass}
        />
      ),
      cell: ({ row }) => (
        <span className='text-xs tabular-nums'>
          {formatUSD(row.original.experience_billing_usd)}
        </span>
      ),
    },
    {
      accessorKey: 'paid_subscription_cost_usd',
      size: 84,
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('Paid Subscription Cost')}
          className={compactHeaderClass}
        />
      ),
      cell: ({ row }) => (
        <span className='text-xs tabular-nums'>
          {formatUSD(row.original.paid_subscription_cost_usd)}
        </span>
      ),
    },
    {
      accessorKey: 'paid_subscription_revenue_usd',
      size: 84,
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('Paid Subscription Revenue')}
          className={compactHeaderClass}
        />
      ),
      cell: ({ row }) => (
        <span className='text-xs tabular-nums'>
          {formatUSD(row.original.paid_subscription_revenue_usd)}
        </span>
      ),
    },
    {
      accessorKey: 'wallet_balance_usd',
      size: 84,
      header: () => <span>{t('Wallet Balance')}</span>,
      cell: ({ row }) => {
        const balance = row.original.wallet_balance_usd
        return balance != null ? (
          <span
            className={`text-xs tabular-nums ${row.original.isTotal ? 'font-semibold' : ''}`}
          >
            {formatUSD(balance)}
          </span>
        ) : (
          <span className='text-muted-foreground text-xs'>—</span>
        )
      },
    },
    {
      accessorKey: 'experience_balance_usd',
      size: 84,
      header: () => <span>{t('Experience Balance')}</span>,
      cell: ({ row }) => {
        const balance = row.original.experience_balance_usd
        return balance != null ? (
          <span
            className={`text-xs tabular-nums ${row.original.isTotal ? 'font-semibold' : ''}`}
          >
            {formatUSD(balance)}
          </span>
        ) : (
          <span className='text-muted-foreground text-xs'>—</span>
        )
      },
    },
    {
      accessorKey: 'paid_subscription_balance_usd',
      size: 92,
      header: () => <span>{t('Paid Subscription Balance')}</span>,
      cell: ({ row }) => {
        const balance = row.original.paid_subscription_balance_usd
        return balance != null ? (
          <span
            className={`text-xs tabular-nums ${row.original.isTotal ? 'font-semibold' : ''}`}
          >
            {formatUSD(balance)}
          </span>
        ) : (
          <span className='text-muted-foreground text-xs'>—</span>
        )
      },
    },
  ]
}
