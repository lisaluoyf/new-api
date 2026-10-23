import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { getSystemOptions, updateSystemOption } from '../api'
import {
  OMNI_MODEL,
  OMNI_RESOLUTIONS,
  OMNI_TOKENS_PER_SECOND,
  mergeOmniPrices,
  readOmniPrices,
} from './gemini-omni-pricing'

export function GeminiOmniPricingSettings() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const query = useQuery({
    queryKey: ['system-options'],
    queryFn: getSystemOptions,
    staleTime: 0,
  })
  const [draft, setDraft] = useState<Record<string, string> | null>(null)
  const options = Object.fromEntries(
    (query.data?.data ?? []).map((row) => [row.key, row.value])
  )
  let error = query.isError || query.data?.success === false
  let stored: Record<string, string> = {}
  try {
    if (query.data?.success) stored = readOmniPrices(options.VideoModelPricing)
  } catch {
    error = true
  }
  const prices = draft ?? stored
  const valid =
    !error &&
    query.data?.success &&
    OMNI_RESOLUTIONS.every(
      (key) =>
        prices[key]?.trim() &&
        Number.isFinite(Number(prices[key])) &&
        Number(prices[key]) > 0
    )
  const save = useMutation({
    mutationFn: async () => {
      if (!valid) throw new Error('Invalid pricing configuration')
      const fresh = await getSystemOptions()
      if (!fresh.success)
        throw new Error('Failed to load pricing configuration')
      const current = Object.fromEntries(
        fresh.data.map((row) => [row.key, row.value])
      )
      const updates = mergeOmniPrices(
        current.ModelPrice,
        current.VideoModelPricing,
        prices
      )
      for (const [key, value] of Object.entries(updates)) {
        const result = await updateSystemOption({ key, value })
        if (!result.success)
          throw new Error(result.message || 'Failed to update setting')
      }
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['system-options'] })
      setDraft(null)
      toast.success(t('Saved'))
    },
    onError: () => toast.error(t('Failed to update setting')),
  })
  return (
    <section
      className='space-y-4 rounded-md border p-4'
      aria-label='Gemini Omni pricing'
    >
      <h3 className='font-semibold'>
        {OMNI_MODEL} · {t('Official prices')}
      </h3>
      <Alert>
        <AlertDescription>
          {t(
            'These are Google official video output prices with audio, in USD per second. Procurement discounts belong to channel ratios. The 720p price is also saved as the model base price.'
          )}
        </AlertDescription>
      </Alert>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Google video output: $17.50 per million tokens. Input: $1.50 per million tokens; text output: $9 per million tokens, billed separately. Verified 2026-10-23.'
        )}{' '}
        <a
          className='underline'
          href='https://cloud.google.com/gemini-enterprise-agent-platform/generative-ai/pricing#gemini-omni'
          target='_blank'
          rel='noreferrer'
        >
          {t('Official pricing source')}
        </a>
      </p>
      {error && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t('Invalid pricing configuration')}
          </AlertDescription>
        </Alert>
      )}
      {query.isPending && <p>{t('Loading...')}</p>}
      <label className='block space-y-1'>
        <span>{t('Model base price (USD / second)')} · 720p</span>
        <Input
          aria-label='Gemini Omni base price'
          type='number'
          min='0.000001'
          step='any'
          value={prices['720P'] ?? ''}
          disabled={!query.data?.success || error || save.isPending}
          onChange={(event) =>
            setDraft({ ...prices, '720P': event.target.value })
          }
        />
      </label>
      <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
        {OMNI_RESOLUTIONS.map((key) => (
          <label key={key} className='block space-y-1 text-sm'>
            <span>
              {key} · {OMNI_TOKENS_PER_SECOND[key]} token/s
            </span>
            <Input
              aria-label={`Gemini Omni official ${key}`}
              type='number'
              min='0.000001'
              step='any'
              value={prices[key] ?? ''}
              disabled={!query.data?.success || error || save.isPending}
              onChange={(event) =>
                setDraft({ ...prices, [key]: event.target.value })
              }
            />
          </label>
        ))}
      </div>
      <Button onClick={() => save.mutate()} disabled={!valid || save.isPending}>
        {save.isPending
          ? t('Saving...')
          : t('Save Gemini Omni official prices')}
      </Button>
    </section>
  )
}
