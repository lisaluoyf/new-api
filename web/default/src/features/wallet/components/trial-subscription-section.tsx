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
import { useEffect, useMemo, useState } from 'react'
import { CheckCircle2, Clock3, CreditCard, Sparkles } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Skeleton } from '@/components/ui/skeleton'
import { getSelfSubscriptionFull } from '@/features/subscriptions/api'
import type {
  PlanRecord,
  UserSubscriptionRecord,
} from '@/features/subscriptions/types'
import { GLASS_CARD_CLS, QUOTA_PER_DOLLAR } from '../constants'

type TrialStatus = 'not_claimed' | 'active' | 'expired' | 'depleted'

function formatUsdAmount(quota: number): string {
  const amount = quota / QUOTA_PER_DOLLAR
  return `$${amount.toFixed(2)}`
}

function formatDateTime(timestamp: number): string {
  if (!timestamp) return '-'
  return new Intl.DateTimeFormat(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date(timestamp * 1000))
}

function resolveTrialSubscription(
  plans: PlanRecord[],
  subscriptions: UserSubscriptionRecord[]
): {
  plan: PlanRecord | null
  subscription: UserSubscriptionRecord | null
} {
  const trialPlans = plans.filter((item) => item.plan.plan_type === 'gpt_trial')
  const plansById = new Map(trialPlans.map((item) => [item.plan.id, item]))
  const allCandidates = subscriptions
    .map((item) => ({
      subscription: item,
      plan: plansById.get(item.subscription.plan_id) || null,
    }))
    .filter((item) => item.plan !== null)
    .sort(
      (a, b) =>
        Number(b.subscription.subscription.end_time || 0) -
        Number(a.subscription.subscription.end_time || 0)
    )

  const latest = allCandidates[0] || null

  return {
    plan: latest?.plan || trialPlans.find((item) => item.plan.enabled) || null,
    subscription: latest?.subscription || null,
  }
}

export function TrialSubscriptionSection() {
  const { i18n } = useTranslation()
  const [loading, setLoading] = useState(true)
  const [plans, setPlans] = useState<PlanRecord[]>([])
  const [subscriptions, setSubscriptions] = useState<UserSubscriptionRecord[]>(
    []
  )

  const isZh = i18n.language.startsWith('zh')
  const copy = isZh
    ? {
        title: '订阅余额',
        emptyTitle: '当前没有可用订阅',
        statusActive: '有效',
        statusNotClaimed: '未领取',
        statusExpired: '已过期',
        statusDepleted: '已用完',
        modelLabel: '使用模型',
        billingLabel: '扣费方式',
        validityLabel: '有效期',
        statusLabel: '状态',
        modelValue: 'GPT 模型',
        billingValue: '官方原价',
        notClaimedValue: '未领取',
        noExpiryValue: '-',
        daysLeft: (days: number, date: string) => `剩余 ${days} 天 (${date})`,
      }
    : {
        title: 'Subscription Balance',
        emptyTitle: 'No active subscription',
        statusActive: 'Active',
        statusNotClaimed: 'Not claimed',
        statusExpired: 'Expired',
        statusDepleted: 'Depleted',
        modelLabel: 'Models',
        billingLabel: 'Billing',
        validityLabel: 'Validity',
        statusLabel: 'Status',
        modelValue: 'GPT models',
        billingValue: 'Official pricing',
        notClaimedValue: 'Not claimed',
        noExpiryValue: '-',
        daysLeft: (days: number, date: string) => `${days} days left (${date})`,
      }

  useEffect(() => {
    let active = true
    const fetchData = async () => {
      try {
        const selfRes = await getSelfSubscriptionFull()
        if (!active) return
        setPlans(selfRes.data?.plans || [])
        setSubscriptions(selfRes.data?.all_subscriptions || [])
      } finally {
        if (active) {
          setLoading(false)
        }
      }
    }

    void fetchData()
    return () => {
      active = false
    }
  }, [])

  const trial = useMemo(
    () => resolveTrialSubscription(plans, subscriptions),
    [plans, subscriptions]
  )

  const subscription = trial.subscription?.subscription || null
  const totalQuota = Number(
    subscription?.amount_total || trial.plan?.plan.total_amount || 0
  )
  const usedQuota = Math.max(0, Number(subscription?.amount_used || 0))
  const remainingQuota = Math.max(totalQuota - usedQuota, 0)
  const expiryDate = subscription?.end_time || 0
  const now = Date.now() / 1000
  const remainingDays =
    expiryDate > now ? Math.max(0, Math.ceil((expiryDate - now) / 86400)) : 0

  let status: TrialStatus = 'not_claimed'
  if (subscription) {
    if (expiryDate > 0 && expiryDate <= now) {
      status = 'expired'
    } else if (totalQuota > 0 && remainingQuota <= 0) {
      status = 'depleted'
    } else {
      status = 'active'
    }
  }

  const statusLabel =
    status === 'active'
      ? copy.statusActive
      : status === 'expired'
        ? copy.statusExpired
        : status === 'depleted'
          ? copy.statusDepleted
          : copy.statusNotClaimed

  const statusClassName =
    status === 'active'
      ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300'
      : status === 'not_claimed'
        ? 'bg-zinc-100 text-zinc-700 dark:bg-zinc-700/60 dark:text-zinc-200'
        : 'bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-300'

  const expiryValue =
    status === 'active'
      ? copy.daysLeft(remainingDays, formatDateTime(expiryDate))
      : expiryDate > 0
        ? formatDateTime(expiryDate)
        : copy.noExpiryValue

  const title = trial.plan?.plan.title || copy.emptyTitle

  if (loading) {
    return (
      <div className={`${GLASS_CARD_CLS} flex h-full flex-col gap-4 px-5 py-4`}>
        <div className='flex items-center gap-4'>
          <Skeleton className='size-11 shrink-0 rounded-xl' />
          <div>
            <Skeleton className='h-3.5 w-28' />
            <Skeleton className='mt-2 h-8 w-40' />
          </div>
        </div>
        <div className='space-y-2'>
          <Skeleton className='h-4 w-48' />
          <Skeleton className='h-4 w-40' />
          <Skeleton className='h-4 w-44' />
        </div>
      </div>
    )
  }

  return (
    <div className={`${GLASS_CARD_CLS} flex h-full flex-col gap-4 px-5 py-4`}>
      <div className='flex items-start justify-between gap-4'>
        <div className='flex min-w-0 items-center gap-4'>
          <div className='flex size-11 shrink-0 items-center justify-center rounded-xl bg-sky-100 dark:bg-sky-500/10'>
            <CreditCard className='size-5 text-sky-600 dark:text-sky-300' />
          </div>
          <div className='min-w-0'>
            <div className='text-muted-foreground text-xs font-medium'>
              {copy.title}
            </div>
            <div className='mt-0.5 font-mono text-2xl font-bold tabular-nums tracking-tight'>
              {formatUsdAmount(remainingQuota)}
              {totalQuota > 0 ? (
                <span className='text-muted-foreground ml-2 text-sm font-medium'>
                  / {formatUsdAmount(totalQuota)}
                </span>
              ) : null}
            </div>
            <div className='text-muted-foreground mt-1 truncate text-sm'>
              {title}
            </div>
          </div>
        </div>

        <div
          className={`inline-flex items-center gap-2 rounded-full px-3 py-1 text-sm font-medium ${statusClassName}`}
        >
          {status === 'active' ? (
            <CheckCircle2 className='size-4' />
          ) : (
            <Sparkles className='size-4' />
          )}
          {statusLabel}
        </div>
      </div>

      <div className='space-y-1.5 text-sm'>
        <div className='flex items-center gap-2'>
          <span className='text-muted-foreground shrink-0'>
            {copy.modelLabel}:
          </span>
          <span className='font-medium'>{copy.modelValue}</span>
        </div>
        <div className='flex items-center gap-2'>
          <span className='text-muted-foreground shrink-0'>
            {copy.billingLabel}:
          </span>
          <span className='font-medium'>{copy.billingValue}</span>
        </div>
        <div className='flex items-center gap-2'>
          <span className='text-muted-foreground shrink-0'>
            {copy.validityLabel}:
          </span>
          <span className='flex items-center gap-1.5 font-medium'>
            <Clock3 className='text-muted-foreground size-4' />
            {expiryValue}
          </span>
        </div>
        <div className='flex items-center gap-2'>
          <span className='text-muted-foreground shrink-0'>
            {copy.statusLabel}:
          </span>
          <span className='font-medium'>{statusLabel}</span>
        </div>
        {status === 'not_claimed' ? (
          <div className='text-muted-foreground pt-1 text-sm'>
            {copy.notClaimedValue}
          </div>
        ) : null}
      </div>
    </div>
  )
}
