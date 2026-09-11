import { ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { TopupRecord } from '../types'

export function PayPalProtectionBadge(props: { record: TopupRecord }) {
  const { t } = useTranslation()
  const protection = props.record.paypal_seller_protection
  if (
    props.record.payment_provider !== 'paypal' ||
    !protection ||
    !['ELIGIBLE', 'PARTIALLY_ELIGIBLE'].includes(protection.status)
  ) {
    return null
  }
  const partial = protection.status === 'PARTIALLY_ELIGIBLE'
  const label = partial
    ? t('Partial seller protection')
    : t('Seller protection')
  const categories = (protection.dispute_categories ?? []).flatMap(
    (category) => {
      if (category === 'ITEM_NOT_RECEIVED') return [t('Item not received')]
      if (category === 'UNAUTHORIZED_TRANSACTION')
        return [t('Unauthorized transaction')]
      return []
    }
  )
  return (
    <span
      title={[
        label,
        ...categories,
        t('Eligibility does not guarantee dispute coverage'),
      ].join(' · ')}
      className={`mt-1 flex w-fit items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium ${partial ? 'border-amber-300 bg-amber-50 text-amber-800 dark:bg-amber-950 dark:text-amber-200' : 'border-green-300 bg-green-50 text-green-800 dark:bg-green-950 dark:text-green-200'}`}
    >
      <ShieldCheck className='size-3' aria-hidden='true' />
      {label}
    </span>
  )
}
