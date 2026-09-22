import { type ColumnDef } from '@tanstack/react-table'
import dayjs from '@/lib/dayjs'
import type { DailyStatsTableRow } from '../types'

function formatDay(timestamp: number): string {
  return dayjs(timestamp * 1000).format('MM-DD')
}

function formatPercent(numerator: number, denominator: number, digits = 0) {
  if (denominator <= 0) return '—'
  return `${((numerator / denominator) * 100).toFixed(digits)}%`
}

function formatCountWithRate(
  count: number,
  total: number,
  digits = 0
): string {
  return `${count} (${formatPercent(count, total, digits)})`
}

function formatUSD(value: number): string {
  const hasCents = Math.abs(value - Math.round(value)) > 0.000001
  return `$${value.toLocaleString('en-US', {
    minimumFractionDigits: hasCents ? 2 : 0,
    maximumFractionDigits: 2,
  })}`
}

function numericCell(value: number | string, emphasized = false) {
  return (
    <span
      className={`text-xs whitespace-nowrap tabular-nums ${emphasized ? 'font-semibold' : ''}`}
    >
      {value}
    </span>
  )
}

export function buildDailyStatsColumns(
  t: (key: string) => string
): ColumnDef<DailyStatsTableRow, unknown>[] {
  return [
    {
      accessorKey: 'day',
      size: 72,
      header: () => <span>{t('Date')}</span>,
      cell: ({ row }) =>
        row.original.isTotal
          ? numericCell(t('Total'), true)
          : numericCell(formatDay(row.original.day)),
    },
    {
      accessorKey: 'uv',
      size: 54,
      header: 'UV',
      cell: ({ row }) =>
        numericCell(row.original.traffic_available ? row.original.uv : '—'),
    },
    {
      accessorKey: 'pv',
      size: 54,
      header: 'PV',
      cell: ({ row }) =>
        numericCell(row.original.traffic_available ? row.original.pv : '—'),
    },
    {
      accessorKey: 'registration_count',
      size: 68,
      header: () => <span>{t('Registrations')}</span>,
      cell: ({ row }) =>
        numericCell(row.original.registration_count, row.original.isTotal),
    },
    {
      accessorKey: 'telegram_registration_count',
      size: 92,
      header: () => <span>{t('Telegram Registrations')}</span>,
      cell: ({ row }) => numericCell(row.original.telegram_registration_count),
    },
    {
      accessorKey: 'google_registration_count',
      size: 92,
      header: () => <span>{t('Google Registrations')}</span>,
      cell: ({ row }) => numericCell(row.original.google_registration_count),
    },
    {
      id: 'referral_share',
      size: 108,
      header: () => <span>{t('Referral Share')}</span>,
      cell: ({ row }) =>
        numericCell(
          formatCountWithRate(
            row.original.referral_registration_count,
            row.original.registration_count,
            1
          )
        ),
    },
    {
      accessorKey: 'paying_user_count',
      size: 78,
      header: () => <span>{t('Paying Users')}</span>,
      cell: ({ row }) => numericCell(row.original.paying_user_count),
    },
    {
      id: 'registration_conversion',
      size: 116,
      header: () => <span>{t('Registration Conversion')}</span>,
      cell: ({ row }) =>
        numericCell(
          formatPercent(
            row.original.same_day_paying_registration_count,
            row.original.registration_count,
            1
          )
        ),
    },
    {
      accessorKey: 'paid_amount_usd',
      size: 88,
      header: () => <span>{t('Paid USD')}</span>,
      cell: ({ row }) => numericCell(formatUSD(row.original.paid_amount_usd)),
    },
    {
      accessorKey: 'commission_usd',
      size: 104,
      header: () => <span>{t('Commission USD')}</span>,
      cell: ({ row }) => numericCell(formatUSD(row.original.commission_usd)),
    },
    {
      id: 'first_purchase_rate',
      size: 104,
      header: () => <span>{t('First Purchase Rate')}</span>,
      cell: ({ row }) =>
        numericCell(
          formatPercent(
            row.original.first_purchase_count,
            row.original.paying_user_count
          )
        ),
    },
    {
      id: 'trial_claims',
      size: 104,
      header: () => <span>{t('Trial Claims')}</span>,
      cell: ({ row }) =>
        numericCell(
          formatCountWithRate(
            row.original.trial_claim_count,
            row.original.registration_count
          )
        ),
    },
    {
      id: 'trial_rejections',
      size: 118,
      header: () => <span>{t('Trial Rejection Rate')}</span>,
      cell: ({ row }) =>
        numericCell(
          formatCountWithRate(
            row.original.trial_rejected_count,
            row.original.registration_count
          )
        ),
    },
    {
      accessorKey: 'benefit_impression_count',
      size: 100,
      header: () => <span>{t('Benefit Impressions')}</span>,
      cell: ({ row }) => numericCell(row.original.benefit_impression_count),
    },
    {
      accessorKey: 'benefit_click_count',
      size: 66,
      header: () => <span>{t('Clicks')}</span>,
      cell: ({ row }) => numericCell(row.original.benefit_click_count),
    },
    {
      id: 'benefit_click_rate',
      size: 82,
      header: () => <span>{t('Click Rate')}</span>,
      cell: ({ row }) =>
        numericCell(
          formatPercent(
            row.original.benefit_click_count,
            row.original.benefit_impression_count
          )
        ),
    },
    {
      accessorKey: 'onboarding_impression_count',
      size: 112,
      header: () => <span>{t('Onboarding Impressions')}</span>,
      cell: ({ row }) => numericCell(row.original.onboarding_impression_count),
    },
    {
      accessorKey: 'key_user_count',
      size: 92,
      header: () => <span>{t('Users with Keys')}</span>,
      cell: ({ row }) => numericCell(row.original.key_user_count),
    },
    {
      id: 'key_rate',
      size: 76,
      header: () => <span>{t('Key Rate')}</span>,
      cell: ({ row }) =>
        numericCell(
          formatPercent(
            row.original.key_user_count,
            row.original.registration_count
          )
        ),
    },
    {
      accessorKey: 'home_click_count',
      size: 104,
      header: () => <span>{t('First Topup Clicks')}</span>,
      cell: ({ row }) => numericCell(row.original.home_click_count),
    },
  ]
}
