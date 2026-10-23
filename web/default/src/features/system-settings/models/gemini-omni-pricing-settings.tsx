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
  OMNI_OPTION,
  OMNI_TIERS,
  mergeOmniBasePrice,
  parseOmniPrices,
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
  const [baseDraft, setBaseDraft] = useState<string | null>(null)
  const options = Object.fromEntries(
    (query.data?.data ?? []).map((row) => [row.key, row.value])
  )
  let error = query.isError || query.data?.success === false
  let stored: Record<string, number> = {}
  let base = ''
  try {
    if (query.data?.success) {
      stored = parseOmniPrices(options[OMNI_OPTION])
      base = String(JSON.parse(options.ModelPrice || '{}')[OMNI_MODEL] ?? '')
    }
  } catch {
    error = true
  }
  const prices =
    draft ??
    Object.fromEntries(
      Object.entries(stored).map(([key, value]) => [key, String(value)])
    )
  const basePrice = baseDraft ?? base
  const valid =
    !error &&
    query.data?.success &&
    [basePrice, ...OMNI_TIERS.map((key) => prices[key])].every(
      (value) =>
        value?.trim() && Number.isFinite(Number(value)) && Number(value) > 0
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
      const tariffs = Object.fromEntries(
        OMNI_TIERS.map((key) => [key, Number(prices[key])])
      )
      parseOmniPrices(JSON.stringify(tariffs))
      const updates = mergeOmniBasePrice(
        current.ModelPrice,
        current.VideoModelPricing,
        Number(basePrice)
      )
      for (const [key, value] of Object.entries({
        ...updates,
        [OMNI_OPTION]: JSON.stringify(tariffs),
      })) {
        const result = await updateSystemOption({ key, value })
        if (!result.success)
          throw new Error(result.message || 'Failed to update setting')
      }
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['system-options'] })
      setDraft(null)
      setBaseDraft(null)
      toast.success(t('Saved'))
    },
    onError: () => toast.error(t('Failed to update setting')),
  })
  return (
    <section
      className='space-y-4 rounded-md border p-4'
      aria-label='Gemini Omni pricing'
    >
      <h3 className='font-semibold'>{OMNI_MODEL}</h3>
      <Alert>
        <AlertDescription>
          {t(
            'Omni route prices are APIMart golden procurement prices in USD per second, not Google list prices. Ext duration tiers show the effective price per second; task price = rate × duration. Channel and account ratios determine retail prices. Final charges reconcile provider usage.'
          )}
        </AlertDescription>
      </Alert>
      {error && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t('Invalid pricing configuration')}
          </AlertDescription>
        </Alert>
      )}
      {query.isPending && <p>{t('Loading...')}</p>}
      <label className='block space-y-1'>
        <span>{t('Model base price (USD / second)')}</span>
        <Input
          aria-label='Gemini Omni base price'
          type='number'
          min='0.000001'
          step='any'
          value={basePrice}
          disabled={!query.data?.success || error || save.isPending}
          onChange={(event) => setBaseDraft(event.target.value)}
        />
      </label>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Google verified 720p video output baseline: $0.10136/second. Other Google resolution list prices are not configured. This base is shared with ModelPrice and video pricing; it is not the channel sale price.'
        )}
      </p>
      {['Official', 'Ext'].map((kind) => (
        <div key={kind} className='space-y-2'>
          <h4 className='font-medium'>
            APIMart {kind} · {kind === 'Official' ? 4 : 20} {t('Specification')}
          </h4>
          <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
            {OMNI_TIERS.filter((key) => key.startsWith(kind + '-')).map(
              (key) => (
                <label key={key} className='block space-y-1 text-sm'>
                  <span>{key.slice(kind.length + 1)}</span>
                  <Input
                    aria-label={`Gemini Omni ${key}`}
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
              )
            )}
          </div>
        </div>
      ))}
      <Button onClick={() => save.mutate()} disabled={!valid || save.isPending}>
        {save.isPending ? t('Saving...') : t('Save Gemini Omni prices')}
      </Button>
    </section>
  )
}
