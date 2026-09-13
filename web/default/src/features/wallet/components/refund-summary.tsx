import { useTranslation } from 'react-i18next'
import { formatQuotaWithCurrency } from '@/lib/currency'
import type { TopupRecord } from '../types'

export function RefundSummary({ record }: { record: TopupRecord }) {
  const { t } = useTranslation()
  const pending = Number(record.refund_frozen_amount || 0)
  const refunded = Number(record.refunded_amount || 0)
  if (pending <= 0 && refunded <= 0) return null

  return (
    <div className='mt-1 space-y-1 text-xs'>
      {pending > 0 && (
        <div className='text-amber-700 dark:text-amber-400'>
          <div>
            {t('Refund pending')}: ${pending.toFixed(2)}
          </div>
          <div>
            {t('Frozen balance')}:{' '}
            {formatQuotaWithCurrency(record.refund_frozen_quota || 0)}
          </div>
        </div>
      )}
      {refunded > 0 && (
        <div className='text-muted-foreground'>
          {t('Refunded')}: ${refunded.toFixed(2)}
        </div>
      )}
    </div>
  )
}
