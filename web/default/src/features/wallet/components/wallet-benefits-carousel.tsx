import { useEffect, useMemo, useRef, useState } from 'react'
import { ChevronLeft, ChevronRight, Clock3, Sparkles } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { getSelfSubscriptionFull } from '@/features/subscriptions/api'
import type { SubscriptionPlan } from '@/features/subscriptions/types'
import { GLASS_CARD_CLS, QUOTA_PER_DOLLAR } from '../constants'
import { TrialSubscriptionSection } from './trial-subscription-section'

type GPTState = {
  plans: SubscriptionPlan[]
  state: {
    subscription?: {
      plan_title_snapshot?: string
      end_time: number
    } | null
    five_hour_used?: number
    seven_day_used?: number
  }
}

function usd(quota: number) {
  return `$${(Number(quota || 0) / QUOTA_PER_DOLLAR).toFixed(2)}`
}

function formatDate(timestamp: number) {
  return new Intl.DateTimeFormat(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date(timestamp * 1000))
}

function GPTSubscriptionCard({ data }: { data: GPTState }) {
  const { t } = useTranslation()
  const current = data.state.subscription
  const lowest = useMemo(
    () => [...data.plans].sort((a, b) => a.price_amount - b.price_amount)[0],
    [data.plans]
  )

  return (
    <div
      className={`${GLASS_CARD_CLS} flex h-[258px] flex-col justify-between px-5 py-4`}
    >
      <div>
        <div className='flex items-start justify-between gap-4'>
          <div className='flex min-w-0 items-center gap-4'>
            <div className='flex size-11 shrink-0 items-center justify-center rounded-xl bg-fuchsia-100 dark:bg-fuchsia-500/10'>
              <Sparkles className='size-5 text-fuchsia-600 dark:text-fuchsia-300' />
            </div>
            <div className='min-w-0'>
              <div className='text-muted-foreground text-xs font-medium'>
                Free Model
              </div>
              <div className='mt-0.5 truncate text-2xl font-bold'>
                {current?.plan_title_snapshot || t('GPT Subscription')}
              </div>
            </div>
          </div>
          <span className='rounded-full bg-fuchsia-50 px-3 py-1 text-sm font-medium text-fuchsia-700 dark:bg-fuchsia-500/10 dark:text-fuchsia-300'>
            {current ? t('Active') : t('Available')}
          </span>
        </div>

        <div className='mt-5 space-y-2 text-sm'>
          {current ? (
            <>
              <div className='flex items-center justify-between gap-3'>
                <span className='text-muted-foreground'>{t('Validity')}</span>
                <span className='flex items-center gap-1.5 font-medium'>
                  <Clock3 className='size-4' /> {formatDate(current.end_time)}
                </span>
              </div>
              <div className='flex items-center justify-between gap-3'>
                <span className='text-muted-foreground'>
                  {t('5-hour usage')}
                </span>
                <span className='font-mono font-medium'>
                  {usd(data.state.five_hour_used || 0)}
                </span>
              </div>
              <div className='flex items-center justify-between gap-3'>
                <span className='text-muted-foreground'>
                  {t('7-day usage')}
                </span>
                <span className='font-mono font-medium'>
                  {usd(data.state.seven_day_used || 0)}
                </span>
              </div>
            </>
          ) : (
            <>
              <p className='text-muted-foreground leading-6'>
                {t(
                  'Official-price GPT usage with rolling limits and wallet fallback.'
                )}
              </p>
              <div className='font-mono text-lg font-semibold'>
                {t('Starting at')} $
                {Number(lowest?.price_amount || 0).toFixed(0)}
                <span className='text-muted-foreground ml-1 text-xs font-normal'>
                  / 30 {t('days')}
                </span>
              </div>
            </>
          )}
        </div>
      </div>

      <Button
        className='w-full bg-fuchsia-600 text-white hover:bg-fuchsia-700'
        onClick={() => window.top?.location.assign('/freemodel')}
      >
        {current ? t('Manage plan') : t('View plans')}
      </Button>
    </div>
  )
}

export function WalletBenefitsCarousel() {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(true)
  const [gpt, setGPT] = useState<GPTState | null>(null)
  const [showTrial, setShowTrial] = useState(false)
  const [index, setIndex] = useState(0)
  const [paused, setPaused] = useState(false)
  const touchStart = useRef<number | null>(null)

  useEffect(() => {
    let active = true
    void Promise.allSettled([
      api.get('/api/subscription/gpt/plans', {
        // This request only decides whether the optional Free Model card is
        // visible. A 403 is expected for users outside the internal allowlist
        // and must not surface as a global error toast.
        skipErrorHandler: true,
        skipBusinessError: true,
      } as Record<string, unknown>),
      getSelfSubscriptionFull(),
    ]).then(([gptResult, allResult]) => {
      if (!active) return
      if (gptResult.status === 'fulfilled' && gptResult.value.data?.success) {
        setGPT(gptResult.value.data.data as GPTState)
      }
      if (allResult.status === 'fulfilled') {
        setShowTrial(
          Boolean(
            allResult.value.data?.plans?.some(
              (item) => item.plan.plan_type === 'gpt_trial' && item.plan.enabled
            )
          )
        )
      }
      setLoading(false)
    })
    return () => {
      active = false
    }
  }, [])

  const slides = useMemo(
    () => [
      ...(gpt
        ? [{ key: 'gpt', node: <GPTSubscriptionCard data={gpt} /> }]
        : []),
      ...(showTrial
        ? [{ key: 'trial', node: <TrialSubscriptionSection /> }]
        : []),
    ],
    [gpt, showTrial]
  )

  useEffect(() => {
    if (slides.length < 2 || paused) return
    const timer = window.setInterval(
      () => setIndex((value) => (value + 1) % slides.length),
      6000
    )
    return () => window.clearInterval(timer)
  }, [paused, slides.length])

  if (loading) {
    return (
      <div className={`${GLASS_CARD_CLS} h-[258px] p-5`}>
        <Skeleton className='h-full w-full' />
      </div>
    )
  }
  if (!slides.length) return null
  const visibleIndex = index % slides.length

  function move(delta: number) {
    setIndex((value) => (value + delta + slides.length) % slides.length)
  }

  return (
    <div
      className='relative h-[286px] min-w-0 overflow-hidden pb-7'
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      onFocusCapture={() => setPaused(true)}
      onBlurCapture={() => setPaused(false)}
      onTouchStart={(event) => {
        touchStart.current = event.touches[0]?.clientX ?? null
        setPaused(true)
      }}
      onTouchEnd={(event) => {
        const start = touchStart.current
        const end = event.changedTouches[0]?.clientX
        touchStart.current = null
        if (start != null && end != null && Math.abs(end - start) > 40) {
          move(end < start ? 1 : -1)
        }
        setPaused(false)
      }}
    >
      <div
        className='flex h-[258px] transition-transform duration-500 ease-out'
        style={{ transform: `translateX(-${visibleIndex * 100}%)` }}
      >
        {slides.map((slide) => (
          <div className='h-[258px] w-full shrink-0' key={slide.key}>
            {slide.node}
          </div>
        ))}
      </div>

      {slides.length > 1 ? (
        <>
          <button
            type='button'
            aria-label={t('Previous benefit')}
            title={t('Previous')}
            className='bg-background/80 absolute top-[112px] left-2 flex size-8 items-center justify-center rounded-full border shadow-sm backdrop-blur'
            onClick={() => move(-1)}
          >
            <ChevronLeft className='size-4' />
          </button>
          <button
            type='button'
            aria-label={t('Next benefit')}
            title={t('Next')}
            className='bg-background/80 absolute top-[112px] right-2 flex size-8 items-center justify-center rounded-full border shadow-sm backdrop-blur'
            onClick={() => move(1)}
          >
            <ChevronRight className='size-4' />
          </button>
          <div className='absolute inset-x-0 bottom-1 flex justify-center gap-2'>
            {slides.map((slide, slideIndex) => (
              <button
                key={slide.key}
                type='button'
                aria-label={t('Show benefit {{number}}', {
                  number: slideIndex + 1,
                })}
                aria-current={slideIndex === visibleIndex}
                className={`h-2 rounded-full transition-all ${slideIndex === visibleIndex ? 'bg-primary w-5' : 'bg-muted-foreground/35 w-2'}`}
                onClick={() => setIndex(slideIndex)}
              />
            ))}
          </div>
        </>
      ) : null}
    </div>
  )
}
