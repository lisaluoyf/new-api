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
import { useMemo } from 'react'
import { type ColumnDef } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { DataTableColumnHeader } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { StatusBadge } from '@/components/status-badge'
import { formatDuration, formatResetPeriod } from '../lib'
import type { PlanRecord } from '../types'
import { DataTableRowActions } from './data-table-row-actions'

const QUOTA_PER_USD = 500_000
const THEORETICAL_PURCHASE_RATE = 0.025

function getPlanDurationDays(plan: PlanRecord['plan']) {
  const value = Math.max(Number(plan.duration_value || 0), 0)
  switch (plan.duration_unit) {
    case 'year':
      return value * 365
    case 'month':
      return value * 30
    case 'day':
      return value
    case 'hour':
      return value / 24
    case 'custom':
      return Math.max(Number(plan.custom_seconds || 0), 0) / 86_400
    default:
      return 0
  }
}

export function useSubscriptionsColumns(): ColumnDef<PlanRecord>[] {
  const { t } = useTranslation()

  return useMemo(
    (): ColumnDef<PlanRecord>[] => [
      {
        accessorFn: (row) => row.plan.id,
        id: 'id',
        meta: { label: 'ID', mobileHidden: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title='ID' />
        ),
        cell: ({ row }) => (
          <span className='text-muted-foreground'>#{row.original.plan.id}</span>
        ),
        size: 60,
      },
      {
        accessorFn: (row) => row.plan.title,
        id: 'title',
        meta: { label: t('Plan'), mobileTitle: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Plan')} />
        ),
        cell: ({ row }) => {
          const plan = row.original.plan
          return (
            <div className='max-w-[200px]'>
              <div className='truncate font-medium'>{plan.title}</div>
              {plan.subtitle && (
                <div className='text-muted-foreground truncate text-xs'>
                  {plan.subtitle}
                </div>
              )}
            </div>
          )
        },
        size: 200,
      },
      {
        accessorFn: (row) => row.plan.price_amount,
        id: 'price',
        meta: { label: t('Price') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Price')} />
        ),
        cell: ({ row }) => (
          <span className='font-semibold text-emerald-600'>
            ${Number(row.original.plan.price_amount || 0).toFixed(2)}
          </span>
        ),
        size: 100,
      },
      {
        id: 'duration',
        meta: { label: t('Validity') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Validity')} />
        ),
        cell: ({ row }) => (
          <span className='text-muted-foreground'>
            {formatDuration(row.original.plan, t)}
          </span>
        ),
        size: 100,
      },
      {
        id: 'reset',
        meta: { label: t('Quota Reset'), mobileHidden: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Quota Reset')} />
        ),
        cell: ({ row }) => (
          <span className='text-muted-foreground'>
            {formatResetPeriod(row.original.plan, t)}
          </span>
        ),
        size: 80,
      },
      {
        id: 'gpt_limits',
        meta: { label: '滚动额度', mobileHidden: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title='滚动额度' />
        ),
        cell: ({ row }) => {
          const plan = row.original.plan
          if (plan.plan_type !== 'gpt_subscription') return '-'
          return (
            <div className='text-muted-foreground text-xs'>
              <div>
                {`5 小时 $${(
                  Number(plan.five_hour_amount || 0) / QUOTA_PER_USD
                ).toFixed(0)}`}
              </div>
              <div>
                {`7 天 $${(
                  Number(plan.seven_day_amount || 0) / QUOTA_PER_USD
                ).toFixed(0)}`}
              </div>
            </div>
          )
        },
        size: 95,
      },
      {
        id: 'coding_allowance',
        meta: { label: 'Coding Plan 额度', mobileHidden: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title='Coding Plan 额度' />
        ),
        cell: ({ row }) => {
          const plan = row.original.plan
          if (plan.plan_type !== 'coding_plan') return '-'
          const modelCount = (() => {
            try {
              return Object.keys(
                JSON.parse(plan.coding_model_multipliers || '{}')
              ).length
            } catch {
              return 0
            }
          })()
          return (
            <div className='text-muted-foreground text-xs'>
              <div>
                ${Number(plan.coding_official_amount_usd || 0).toFixed(2)}
              </div>
              <div>{modelCount} 个模型</div>
            </div>
          )
        },
        size: 110,
      },
      {
        id: 'gpt_margin',
        meta: { label: '2.5% 成本预览', mobileHidden: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title='2.5% 成本预览' />
        ),
        cell: ({ row }) => {
          const plan = row.original.plan
          if (plan.plan_type !== 'gpt_subscription') return '-'
          const durationDays = getPlanDurationDays(plan)
          const previewDays = durationDays > 0 ? durationDays : 30
          const sevenDayUSD = Number(plan.seven_day_amount || 0) / QUOTA_PER_USD
          const fullUseOfficialUSD = sevenDayUSD * (previewDays / 7)
          const fullUseCostUSD = fullUseOfficialUSD * THEORETICAL_PURCHASE_RATE
          const priceUSD = Number(plan.price_amount || 0)
          const grossMargin =
            priceUSD > 0 ? ((priceUSD - fullUseCostUSD) / priceUSD) * 100 : null
          const previewDaysLabel = Number.isInteger(previewDays)
            ? previewDays.toFixed(0)
            : previewDays.toFixed(1)
          return (
            <div className='text-xs'>
              <div
                className={
                  grossMargin != null && grossMargin < 0
                    ? 'text-destructive font-medium'
                    : 'font-medium text-emerald-600'
                }
              >
                {previewDaysLabel} 天满用理论毛利率{' '}
                {grossMargin == null ? '—' : `${grossMargin.toFixed(1)}%`}
              </div>
              <div className='text-muted-foreground'>
                {previewDaysLabel} 天满用理论成本 ${fullUseCostUSD.toFixed(2)}
              </div>
            </div>
          )
        },
        size: 135,
      },
      {
        accessorFn: (row) => row.plan.sort_order,
        id: 'sort_order',
        meta: { label: t('Priority'), mobileHidden: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Priority')} />
        ),
        cell: ({ row }) => (
          <span className='text-muted-foreground'>
            {row.original.plan.sort_order}
          </span>
        ),
        size: 80,
      },
      {
        accessorFn: (row) => row.plan.enabled,
        id: 'enabled',
        meta: { label: t('Status'), mobileBadge: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Status')} />
        ),
        cell: ({ row }) =>
          !row.original.plan.enabled ? (
            <StatusBadge
              label={t('Disable')}
              variant='neutral'
              copyable={false}
            />
          ) : row.original.plan.sold_out ? (
            <StatusBadge
              label={t('Sold out')}
              variant='danger'
              copyable={false}
            />
          ) : (
            <StatusBadge
              label={t('Enable')}
              variant='success'
              copyable={false}
            />
          ),
        size: 80,
      },
      {
        id: 'payment',
        meta: { label: t('Payment Channel'), mobileHidden: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Payment Channel')} />
        ),
        cell: ({ row }) => {
          const plan = row.original.plan
          return (
            <div className='flex gap-1'>
              {plan.stripe_price_id && (
                <StatusBadge
                  label='Stripe'
                  variant='neutral'
                  copyable={false}
                />
              )}
              {plan.creem_product_id && (
                <StatusBadge label='Creem' variant='neutral' copyable={false} />
              )}
            </div>
          )
        },
        size: 140,
      },
      {
        id: 'total_amount',
        meta: { label: t('Total Quota'), mobileHidden: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Total Quota')} />
        ),
        cell: ({ row }) => {
          const total = Number(row.original.plan.total_amount || 0)
          return (
            <span className='text-muted-foreground'>
              {total > 0 ? total : t('Unlimited')}
            </span>
          )
        },
        size: 100,
      },
      {
        id: 'upgrade_group',
        meta: { label: t('Upgrade Group'), mobileHidden: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Upgrade Group')} />
        ),
        cell: ({ row }) => {
          const group = row.original.plan.upgrade_group
          if (!group) {
            return (
              <span className='text-muted-foreground'>{t('No Upgrade')}</span>
            )
          }
          return <GroupBadge group={group} />
        },
        size: 100,
      },
      {
        id: 'actions',
        cell: ({ row }) => <DataTableRowActions row={row} />,
        size: 80,
      },
    ],
    [t]
  )
}
