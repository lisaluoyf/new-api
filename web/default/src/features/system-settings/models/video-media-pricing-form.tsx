import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { useUpdateOption } from '../hooks/use-update-option'

export function VideoMediaPricingForm({ defaultValue }: { defaultValue: string }) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [value, setValue] = useState(defaultValue || '{}')
  useEffect(() => setValue(defaultValue || '{}'), [defaultValue])
  const save = async () => {
    try {
      const parsed = JSON.parse(value)
      if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error('Expected a JSON object')
      await updateOption.mutateAsync({ key: 'VideoModelPricing', value: JSON.stringify(parsed) })
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Invalid JSON'))
    }
  }
  return (
    <div className='space-y-4'>
      <div>
        <h3 className='text-base font-medium'>{t('Video / Media Model Pricing')}</h3>
        <p className='text-muted-foreground text-sm'>{t('Configure official per-second prices by model and resolution. The first configured price is the base price used for billing ratios.')}</p>
      </div>
      <Textarea value={value} onChange={(event) => setValue(event.target.value)} className='min-h-[260px] font-mono text-sm' placeholder='{"minimax-h3":{"unit":"second","prices":{"768P":0.08,"2K":0.13}}}' />
      <Button onClick={save} disabled={updateOption.isPending}>{t('Save')}</Button>
    </div>
  )
}
