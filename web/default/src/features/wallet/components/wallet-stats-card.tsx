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
import { useQuery } from '@tanstack/react-query'
import { WalletCards } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatQuota } from '@/lib/format'
import { api } from '@/lib/api'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { Skeleton } from '@/components/ui/skeleton'
import { GLASS_CARD_CLS } from '../constants'
import type { UserWalletData } from '../types'

interface WalletStatsCardProps {
  user: UserWalletData | null
  loading?: boolean
}

interface MarketplacePricingItem {
  channel_id: number
  user_price?: number | null
  official_input_price?: number | null
}

const FEATURED_MODEL_ID = 'gpt-5.6-sol'
const FEATURED_MODEL_LABEL = 'GPT 5.6 Sol'

export function WalletStatsCard({ user, loading }: WalletStatsCardProps) {
  const { t, i18n } = useTranslation()
  const isChinese = (i18n.resolvedLanguage || i18n.language || '')
    .toLowerCase()
    .startsWith('zh')
  const copy = {
    title: t('Wallet Balance'),
    models: t('Models'),
    modelsValue: t('All models'),
    billing: t('Billing'),
    billingValue: t('APIMaster discounted pricing'),
    featuredPriceLabel: isChinese ? '实时折扣价：' : 'live price:',
    viewAllDiscounts: isChinese ? '查看完整折扣' : 'View all discounts',
  }

  const { data: featuredMarketplaceItems = [] } = useQuery({
    queryKey: ['featured-marketplace-pricing', FEATURED_MODEL_ID],
    queryFn: async () => {
      const res = await api.get('/api/public/marketplace', {
        params: { model: FEATURED_MODEL_ID },
      })
      return (res.data?.data ?? []) as MarketplacePricingItem[]
    },
    staleTime: 5 * 60 * 1000,
  })

  const featuredPricing = useMemo(() => {
    const bestItem = featuredMarketplaceItems
      .filter(
        (item) =>
          Number.isFinite(item.user_price) &&
          Number.isFinite(item.official_input_price) &&
          Number(item.user_price) > 0 &&
          Number(item.official_input_price) > 0
      )
      .sort((a, b) => Number(a.user_price) - Number(b.user_price))[0]

    if (!bestItem) {
      return null
    }

    const discountedPrice = Number(bestItem.user_price)
    const officialPrice = Number(bestItem.official_input_price)
    if (
      !Number.isFinite(discountedPrice) ||
      !Number.isFinite(officialPrice) ||
      officialPrice <= 0 ||
      discountedPrice <= 0
    ) {
      return null
    }

    return {
      modelLabel: FEATURED_MODEL_LABEL,
      discountPct: Math.max(
        0,
        Math.round((1 - discountedPrice / officialPrice) * 100)
      ),
      discountedPriceLabel: `${formatBillingCurrencyFromUSD(discountedPrice, {
        digitsLarge: 4,
        digitsSmall: 6,
        abbreviate: false,
      })}/M`,
      officialPriceLabel: `${formatBillingCurrencyFromUSD(officialPrice, {
        digitsLarge: 4,
        digitsSmall: 6,
        abbreviate: false,
      })}/M`,
    }
  }, [featuredMarketplaceItems])

  if (loading) {
    return (
      <div className={`${GLASS_CARD_CLS} flex h-full flex-col gap-4 px-5 py-4`}>
        <div className='flex items-center gap-4'>
          <Skeleton className='size-11 shrink-0 rounded-xl' />
          <div>
            <Skeleton className='h-3.5 w-24' />
            <Skeleton className='mt-2 h-8 w-32' />
          </div>
        </div>
        <div className='space-y-2'>
          <Skeleton className='h-4 w-44' />
          <Skeleton className='h-4 w-52' />
        </div>
      </div>
    )
  }

  return (
    <div className={`${GLASS_CARD_CLS} flex h-full flex-col gap-4 px-5 py-4`}>
      <div className='flex items-center gap-4'>
        <div className='flex size-11 shrink-0 items-center justify-center rounded-xl bg-green-100 dark:bg-green-900/30'>
          <WalletCards className='size-5 text-green-600' />
        </div>
        <div>
          <div className='text-muted-foreground text-xs font-medium'>
            {copy.title}
          </div>
          <div className='mt-0.5 font-mono text-2xl font-bold tracking-tight tabular-nums'>
            {formatQuota(user?.quota ?? 0)}
          </div>
        </div>
      </div>

      <div className='space-y-1.5 text-sm'>
        <div className='flex items-center gap-2'>
          <span className='text-muted-foreground shrink-0'>{copy.models}:</span>
          <span className='font-medium'>{copy.modelsValue}</span>
        </div>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='text-muted-foreground shrink-0'>
            {copy.billing}:
          </span>
          <span className='inline-flex items-center rounded-md bg-emerald-50 px-2 py-0.5 font-medium text-emerald-700 ring-1 ring-emerald-200 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-500/20'>
            {copy.billingValue}
          </span>
          {featuredPricing ? (
            <span className='text-muted-foreground flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1 text-xs sm:text-sm'>
              <span className='shrink-0'>
                {featuredPricing.modelLabel} {copy.featuredPriceLabel}
              </span>
              <span className='text-foreground font-semibold'>
                {featuredPricing.discountPct}% off
              </span>
              <span className='text-foreground font-mono font-semibold'>
                {featuredPricing.discountedPriceLabel}
              </span>
              <span className='text-muted-foreground/70 font-mono line-through'>
                {featuredPricing.officialPriceLabel}
              </span>
              <a
                href='https://apimaster.ai/'
                className='font-medium text-sky-600 underline underline-offset-4 transition-colors hover:text-sky-500 dark:text-sky-300 dark:hover:text-sky-200'
              >
                {copy.viewAllDiscounts}
              </a>
            </span>
          ) : null}
        </div>
      </div>
    </div>
  )
}
