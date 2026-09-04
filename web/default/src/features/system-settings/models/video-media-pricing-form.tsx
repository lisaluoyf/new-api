import { useCallback, useEffect, useMemo, useState } from 'react'
import { Code2, Copy, Eye, Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { useUpdateOption } from '../hooks/use-update-option'

type PricingRow = {
  id: number
  model: string
  unit: string
  basePrice?: number
  baseVariant?: string
  prices: Record<string, number>
  officialPrices: Record<string, number>
}

const DEFAULT_PRICING = {
  'minimax-h3': { unit: 'second', prices: { '768P': 0.08, '2K': 0.13 } },
  'kling-v3-omni': {
    unit: 'second',
    prices: {
      base: 0.084,
      sound: 0.112,
      video: 0.126,
      pro: 0.112,
      'pro-sound': 0.14,
      'pro-video': 0.168,
      '4k': 0.5357,
      '4k-sound': 0.5357,
    },
  },
  'doubao-seedance-2.0': {
    unit: 'second',
    base_price: 0.142,
    base_variant: '720P',
    prices: {
      '480P': 0.066,
      '480P-input': 0.04,
      '720P': 0.142,
      '720P-input': 0.08584,
      '1080P': 0.3544,
      '1080P-input': 0.21568,
      '4K': 0.722,
      '4K-input': 0.44432,
    },
    official_prices: {
      '480P': 0.0825,
      '480P-input': 0.05,
      '720P': 0.1775,
      '720P-input': 0.1073,
      '1080P': 0.443,
      '1080P-input': 0.2696,
      '4K': 0.9025,
      '4K-input': 0.5554,
    },
  },
  'seedance-2.5': {
    unit: 'second',
    base_price: 0.216,
    base_variant: '720P',
    prices: {
      '480P': 0.09608,
      '480P-input': 0.0576,
      '720P': 0.216,
      '720P-input': 0.1296,
      '1080P': 0.38488,
      '1080P-input': 0.22992,
    },
    official_prices: {
      '480P': 0.1201,
      '480P-input': 0.072,
      '720P': 0.27,
      '720P-input': 0.162,
      '1080P': 0.4811,
      '1080P-input': 0.2874,
    },
  },
}

function parsePricing(raw: string | undefined): Record<
  string,
  {
    unit?: string
    base_price?: number
    base_variant?: string
    prices?: Record<string, number>
    official_prices?: Record<string, number>
  }
> {
  if (!raw) return DEFAULT_PRICING
  try {
    const parsed = JSON.parse(raw) as unknown
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      const pricing = parsed as Record<
        string,
        {
          unit?: string
          base_price?: number
          base_variant?: string
          prices?: Record<string, number>
          official_prices?: Record<string, number>
        }
      >
      if (!pricing['doubao-seedance-2.0']) {
        pricing['doubao-seedance-2.0'] = DEFAULT_PRICING['doubao-seedance-2.0']
      } else {
        const configured = pricing['doubao-seedance-2.0']
        const defaults = DEFAULT_PRICING['doubao-seedance-2.0']
        pricing['doubao-seedance-2.0'] = {
          ...defaults,
          ...configured,
          prices: { ...defaults.prices, ...configured.prices },
          official_prices: {
            ...defaults.official_prices,
            ...configured.official_prices,
          },
        }
      }
      if (!pricing['seedance-2.5'])
        pricing['seedance-2.5'] = DEFAULT_PRICING['seedance-2.5']
      else
        pricing['seedance-2.5'] = {
          ...DEFAULT_PRICING['seedance-2.5'],
          ...pricing['seedance-2.5'],
          prices: {
            ...DEFAULT_PRICING['seedance-2.5'].prices,
            ...pricing['seedance-2.5'].prices,
          },
          official_prices: {
            ...DEFAULT_PRICING['seedance-2.5'].official_prices,
            ...pricing['seedance-2.5'].official_prices,
          },
        }
      return pricing
    }
  } catch {
    // fall through to defaults
  }
  return DEFAULT_PRICING
}

function objectToRows(
  value: Record<
    string,
    {
      unit?: string
      base_price?: number
      base_variant?: string
      prices?: Record<string, number>
      official_prices?: Record<string, number>
    }
  >
): PricingRow[] {
  return Object.entries(value).map(([model, config], index) => ({
    id: index + 1,
    model,
    unit: config.unit || 'second',
    basePrice: config.base_price,
    baseVariant: config.base_variant,
    prices: config.prices || {},
    officialPrices: config.official_prices || {},
  }))
}

function rowsToObject(rows: PricingRow[]) {
  const result: Record<
    string,
    {
      unit: string
      base_price?: number
      base_variant?: string
      prices: Record<string, number>
      official_prices?: Record<string, number>
    }
  > = {}
  rows.forEach((row) => {
    const model = row.model.trim()
    if (model) {
      result[model] = {
        unit: row.unit || 'second',
        prices: row.prices,
        official_prices: row.officialPrices,
      }
      if (row.basePrice && row.basePrice > 0) {
        result[model].base_price = row.basePrice
      }
      if (row.baseVariant?.trim()) {
        result[model].base_variant = row.baseVariant.trim()
      }
    }
  })
  return result
}

export function VideoMediaPricingForm({
  defaultValue,
}: {
  defaultValue: string
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [editMode, setEditMode] = useState<'visual' | 'json'>('visual')
  const [rows, setRows] = useState<PricingRow[]>([])
  const [jsonText, setJsonText] = useState('')
  const [jsonError, setJsonError] = useState('')
  const [nextId, setNextId] = useState(1)

  useEffect(() => {
    const initial = objectToRows(parsePricing(defaultValue))
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setRows(initial)
    setJsonText(JSON.stringify(rowsToObject(initial), null, 2))
    setJsonError('')
    setNextId(initial.length + 1)
  }, [defaultValue])

  const currentValue = useMemo(() => rowsToObject(rows), [rows])
  const syncRows = useCallback((next: PricingRow[]) => {
    setRows(next)
    setJsonText(JSON.stringify(rowsToObject(next), null, 2))
    setJsonError('')
  }, [])

  const updateRow = (id: number, patch: Partial<PricingRow>) =>
    syncRows(rows.map((row) => (row.id === id ? { ...row, ...patch } : row)))

  const updatePrice = (id: number, resolution: string, price: number) =>
    updateRow(id, {
      prices: {
        ...rows.find((row) => row.id === id)?.prices,
        [resolution]: price,
      },
    })

  const updateOfficialPrice = (id: number, resolution: string, price: number) =>
    updateRow(id, {
      officialPrices: {
        ...rows.find((row) => row.id === id)?.officialPrices,
        [resolution]: price,
      },
    })

  const addModel = () => {
    setNextId((id) => id + 1)
    syncRows([
      ...rows,
      {
        id: nextId,
        model: '',
        unit: 'second',
        prices: { '768P': 0 },
        officialPrices: { '768P': 0 },
      },
    ])
  }

  const addResolution = (id: number) => {
    const row = rows.find((item) => item.id === id)
    if (!row) return
    let name = '768P'
    let index = 1
    while (row.prices[name] !== undefined) name = `resolution-${index++}`
    updatePrice(id, name, 0)
  }

  const removeModel = (id: number) =>
    syncRows(rows.filter((row) => row.id !== id))
  const removeResolution = (id: number, resolution: string) => {
    const row = rows.find((item) => item.id === id)
    if (!row) return
    const prices = { ...row.prices }
    delete prices[resolution]
    const officialPrices = { ...row.officialPrices }
    delete officialPrices[resolution]
    updateRow(id, { prices, officialPrices })
  }

  const handleJsonChange = (value: string) => {
    setJsonText(value)
    try {
      const parsed = JSON.parse(value) as unknown
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
        throw new Error(t('JSON must be an object'))
      const next = objectToRows(
        parsed as Record<
          string,
          {
            unit?: string
            base_price?: number
            base_variant?: string
            prices?: Record<string, number>
            official_prices?: Record<string, number>
          }
        >
      )
      setRows(next)
      setNextId(next.length + 1)
      setJsonError('')
    } catch (error) {
      setJsonError(error instanceof Error ? error.message : t('Invalid JSON'))
    }
  }

  const handleSave = async () => {
    if (jsonError) {
      toast.error(t('Please fix JSON errors before saving'))
      return
    }
    await updateOption.mutateAsync({
      key: 'VideoModelPricing',
      value: JSON.stringify(currentValue),
    })
  }

  const copyJson = async () => {
    await navigator.clipboard.writeText(jsonText)
    toast.success(t('Copied to clipboard'))
  }

  return (
    <div className='space-y-4'>
      <Alert>
        <AlertDescription className='space-y-1 text-sm'>
          <div>
            {t(
              'Configure video and media model prices by model, billing unit, and resolution.'
            )}
          </div>
          <div>
            {t(
              'When a base price is set, it is used as the billing calculation base; resolution prices are converted into ratios automatically.'
            )}
          </div>
        </AlertDescription>
      </Alert>

      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex gap-2'>
          {editMode === 'visual' ? (
            <Button variant='outline' size='sm' onClick={addModel}>
              <Plus className='mr-2 h-4 w-4' />
              {t('Add model')}
            </Button>
          ) : (
            <Button variant='ghost' size='sm' onClick={copyJson}>
              <Copy className='mr-2 h-4 w-4' />
              {t('Copy')}
            </Button>
          )}
        </div>
        <Button
          variant='outline'
          size='sm'
          onClick={() =>
            setEditMode((mode) => (mode === 'visual' ? 'json' : 'visual'))
          }
        >
          {editMode === 'visual' ? (
            <>
              <Code2 className='mr-2 h-4 w-4' />
              {t('Advanced JSON')}
            </>
          ) : (
            <>
              <Eye className='mr-2 h-4 w-4' />
              {t('Visual editor')}
            </>
          )}
        </Button>
      </div>

      {editMode === 'visual' ? (
        <div className='space-y-3'>
          {rows.length === 0 && (
            <p className='text-muted-foreground py-8 text-center text-sm'>
              {t('No media models configured')}
            </p>
          )}
          {rows.map((row) => (
            <div key={row.id} className='rounded-md border p-4'>
              <div className='grid gap-3 md:grid-cols-[1fr_160px_auto]'>
                <Input
                  value={row.model}
                  placeholder='minimax-h3'
                  aria-label={t('Model')}
                  onChange={(e) => updateRow(row.id, { model: e.target.value })}
                />
                <select
                  className='border-input bg-background h-8 rounded-lg border px-2 text-sm'
                  value={row.unit}
                  aria-label={t('Billing unit')}
                  onChange={(e) => updateRow(row.id, { unit: e.target.value })}
                >
                  <option value='second'>{t('Per second')}</option>
                  <option value='minute'>{t('Per minute')}</option>
                  <option value='request'>{t('Per request')}</option>
                </select>
                <Button
                  variant='ghost'
                  size='icon'
                  onClick={() => removeModel(row.id)}
                  aria-label={t('Delete')}
                >
                  <Trash2 className='text-destructive h-4 w-4' />
                </Button>
              </div>
              <div className='mt-3 grid gap-2 md:grid-cols-[1fr_160px_160px_auto]'>
                <label
                  className='flex items-center text-sm font-medium'
                  htmlFor={`media-base-price-${row.id}`}
                >
                  {t('Base Price')}
                </label>
                <Input
                  id={`media-base-price-${row.id}`}
                  type='number'
                  min={0}
                  step='0.0001'
                  value={row.basePrice ?? ''}
                  placeholder='0.142'
                  onChange={(e) =>
                    updateRow(row.id, {
                      basePrice:
                        e.target.value === ''
                          ? undefined
                          : Number(e.target.value),
                    })
                  }
                />
                <Input
                  value={row.baseVariant ?? ''}
                  placeholder='720P'
                  aria-label={t('Base Variant')}
                  onChange={(e) =>
                    updateRow(row.id, { baseVariant: e.target.value })
                  }
                />
                <span />
              </div>
              <div className='mt-3 space-y-2'>
                <div className='text-muted-foreground hidden gap-2 text-xs md:grid md:grid-cols-[1fr_160px_160px_auto]'>
                  <span>{t('Resolution')}</span>
                  <span>{t('Procurement Price')}</span>
                  <span>{t('Official Price')}</span>
                  <span />
                </div>
                {Object.entries(row.prices).map(([resolution, price]) => (
                  <div
                    key={resolution}
                    className='grid gap-2 md:grid-cols-[1fr_160px_160px_auto]'
                  >
                    <Input
                      value={resolution}
                      aria-label={t('Resolution')}
                      onChange={(e) => {
                        const prices = { ...row.prices }
                        const officialPrices = { ...row.officialPrices }
                        delete prices[resolution]
                        delete officialPrices[resolution]
                        prices[e.target.value] = price
                        officialPrices[e.target.value] =
                          row.officialPrices[resolution] || 0
                        updateRow(row.id, { prices, officialPrices })
                      }}
                    />
                    <Input
                      type='number'
                      min={0}
                      step='0.0001'
                      value={price}
                      aria-label={t('Price')}
                      onChange={(e) =>
                        updatePrice(
                          row.id,
                          resolution,
                          Number(e.target.value) || 0
                        )
                      }
                    />
                    <Input
                      type='number'
                      min={0}
                      step='0.0001'
                      value={row.officialPrices[resolution] ?? 0}
                      aria-label={t('Official Price')}
                      onChange={(e) =>
                        updateOfficialPrice(
                          row.id,
                          resolution,
                          Number(e.target.value) || 0
                        )
                      }
                    />
                    <Button
                      variant='ghost'
                      size='icon'
                      onClick={() => removeResolution(row.id, resolution)}
                      aria-label={t('Delete')}
                    >
                      <Trash2 className='text-destructive h-4 w-4' />
                    </Button>
                  </div>
                ))}
                <Button
                  variant='ghost'
                  size='sm'
                  onClick={() => addResolution(row.id)}
                >
                  <Plus className='mr-2 h-4 w-4' />
                  {t('Add resolution')}
                </Button>
              </div>
            </div>
          ))}
        </div>
      ) : (
        <div className='space-y-2'>
          <Textarea
            value={jsonText}
            onChange={(e) => handleJsonChange(e.target.value)}
            className='min-h-[260px] font-mono text-sm'
            spellCheck={false}
          />
          {jsonError && <p className='text-destructive text-sm'>{jsonError}</p>}
        </div>
      )}

      <div className='flex justify-end'>
        <Button
          onClick={handleSave}
          disabled={updateOption.isPending || !!jsonError}
        >
          {t('Save media pricing')}
        </Button>
      </div>
    </div>
  )
}
