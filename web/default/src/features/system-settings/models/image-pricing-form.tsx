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
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Code2, Copy, Eye, Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { useUpdateOption } from '../hooks/use-update-option'

type ImagePricingConfig = {
  unit?: string
  base_price?: number
  base_variant?: string
  prices?: Record<string, number>
}

type PricingRow = {
  id: number
  model: string
  basePrice?: number
  baseVariant: string
  prices: Record<string, number>
}

const DEFAULT_PRICING: Record<string, ImagePricingConfig> = {
  'gpt-image-2': {
    unit: 'image',
    base_price: 0.25,
    base_variant: '1K',
    prices: { '1K': 0.25, '2K': 0.3, '4K': 0.6 },
  },
}

function parsePricing(raw?: string): Record<string, ImagePricingConfig> {
  if (!raw) return DEFAULT_PRICING
  try {
    const parsed = JSON.parse(raw) as unknown
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, ImagePricingConfig>
    }
  } catch {
    // Fall back to the safe built-in pricing table.
  }
  return DEFAULT_PRICING
}

function objectToRows(value: Record<string, ImagePricingConfig>): PricingRow[] {
  return Object.entries(value).map(([model, config], index) => ({
    id: index + 1,
    model,
    basePrice: config.base_price,
    baseVariant: config.base_variant || '1K',
    prices: config.prices || {},
  }))
}

function rowsToObject(rows: PricingRow[]) {
  const result: Record<string, ImagePricingConfig> = {}
  rows.forEach((row) => {
    const model = row.model.trim()
    if (!model) return
    result[model] = {
      unit: 'image',
      base_variant: row.baseVariant.trim() || '1K',
      prices: row.prices,
    }
    if (row.basePrice && row.basePrice > 0) {
      result[model].base_price = row.basePrice
    }
  })
  return result
}

export function ImagePricingForm({ defaultValue }: { defaultValue: string }) {
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

  const updatePrice = (id: number, resolution: string, price: number) => {
    const row = rows.find((item) => item.id === id)
    if (!row) return
    updateRow(id, { prices: { ...row.prices, [resolution]: price } })
  }

  const addModel = () => {
    syncRows([
      ...rows,
      {
        id: nextId,
        model: '',
        basePrice: 0,
        baseVariant: '1K',
        prices: { '1K': 0, '2K': 0, '4K': 0 },
      },
    ])
    setNextId((id) => id + 1)
  }

  const addResolution = (id: number) => {
    const row = rows.find((item) => item.id === id)
    if (!row) return
    let name = '1K'
    let index = 1
    while (row.prices[name] !== undefined) name = `resolution-${index++}`
    updatePrice(id, name, 0)
  }

  const removeResolution = (id: number, resolution: string) => {
    const row = rows.find((item) => item.id === id)
    if (!row) return
    const prices = { ...row.prices }
    delete prices[resolution]
    updateRow(id, { prices })
  }

  const handleJsonChange = (value: string) => {
    setJsonText(value)
    try {
      const parsed = JSON.parse(value) as unknown
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        throw new Error(t('JSON must be an object'))
      }
      const next = objectToRows(parsed as Record<string, ImagePricingConfig>)
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
      key: 'ImageModelPricing',
      value: JSON.stringify(currentValue),
    })
  }

  const copyJson = async () => {
    await navigator.clipboard.writeText(jsonText)
    toast.success(t('Copied'))
  }

  return (
    <div className='space-y-4'>
      <Alert>
        <AlertDescription className='space-y-1 text-sm'>
          <div>{t('Configure image model base prices by resolution.')}</div>
          <div>
            {t(
              'User price equals the selected resolution base price multiplied by channel group, recharge, APIMaster/model, and user group ratios.'
            )}
          </div>
        </AlertDescription>
      </Alert>

      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div>
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
          {rows.map((row) => (
            <div key={row.id} className='rounded-md border p-4'>
              <div className='grid gap-3 md:grid-cols-[1fr_160px_160px_auto]'>
                <Input
                  value={row.model}
                  placeholder='gpt-image-2'
                  aria-label={t('Model')}
                  onChange={(e) => updateRow(row.id, { model: e.target.value })}
                />
                <Input
                  type='number'
                  min={0}
                  step='0.0001'
                  value={row.basePrice ?? ''}
                  placeholder='0.25'
                  aria-label={t('Base Price')}
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
                  value={row.baseVariant}
                  placeholder='1K'
                  aria-label={t('Base Variant')}
                  onChange={(e) =>
                    updateRow(row.id, { baseVariant: e.target.value })
                  }
                />
                <Button
                  variant='ghost'
                  size='icon'
                  onClick={() =>
                    syncRows(rows.filter((item) => item.id !== row.id))
                  }
                  aria-label={t('Delete')}
                >
                  <Trash2 className='text-destructive h-4 w-4' />
                </Button>
              </div>

              <div className='mt-3 space-y-2'>
                <div className='text-muted-foreground hidden gap-2 text-xs md:grid md:grid-cols-[1fr_180px_auto]'>
                  <span>{t('Resolution')}</span>
                  <span>{t('Base Price')}</span>
                  <span />
                </div>
                {Object.entries(row.prices).map(([resolution, price]) => (
                  <div
                    key={resolution}
                    className='grid gap-2 md:grid-cols-[1fr_180px_auto]'
                  >
                    <Input
                      value={resolution}
                      aria-label={t('Resolution')}
                      onChange={(e) => {
                        const prices = { ...row.prices }
                        delete prices[resolution]
                        prices[e.target.value] = price
                        updateRow(row.id, { prices })
                      }}
                    />
                    <Input
                      type='number'
                      min={0}
                      step='0.0001'
                      value={price}
                      aria-label={t('Base Price')}
                      onChange={(e) =>
                        updatePrice(
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
                  variant='outline'
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
            className='min-h-[360px] font-mono text-xs'
          />
          {jsonError && <p className='text-destructive text-sm'>{jsonError}</p>}
        </div>
      )}

      <div className='flex justify-end'>
        <Button
          onClick={handleSave}
          disabled={updateOption.isPending || !!jsonError}
        >
          {updateOption.isPending ? t('Saving...') : t('Save')}
        </Button>
      </div>
    </div>
  )
}
