import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'

interface Props {
  channelId: number
  model: string
  options: string[]
  selected: string[]
  onSaved: (values: string[]) => void
}

export function SeedanceResolutions({
  channelId,
  model,
  options,
  selected,
  onSaved,
}: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [values, setValues] = useState(selected)
  const mutation = useMutation({
    mutationFn: async () => {
      const response = await api.put('/api/admin/channel-data/resolutions', {
        channel_id: channelId,
        model,
        resolutions: values,
      })
      if (!response.data?.success)
        throw new Error(response.data?.message || t('Save failed'))
      return [...values]
    },
    onSuccess: (saved) => {
      onSaved(saved)
      setOpen(false)
    },
  })
  return (
    <Popover
      open={open}
      onOpenChange={(value) => {
        if (!mutation.isPending) {
          setOpen(value)
          if (value) {
            setValues(selected)
            mutation.reset()
          }
        }
      }}
    >
      <PopoverTrigger
        render={
          <Button
            variant='outline'
            size='sm'
            aria-label={t('Seedance resolution selection')}
          />
        }
      >
        {selected.length === options.length
          ? t('All resolutions')
          : selected.join(', ') || t('No resolutions')}
      </PopoverTrigger>
      <PopoverContent className='w-72'>
        <p className='text-muted-foreground mb-3 text-xs'>
          {t(
            'Unchecked resolutions fall back to another channel. Input means reference video.'
          )}
        </p>
        <div className='grid grid-cols-2 gap-2'>
          {options.map((option) => (
            <label key={option} className='flex items-center gap-2 text-sm'>
              <input
                type='checkbox'
                checked={values.includes(option)}
                disabled={mutation.isPending}
                onChange={(event) =>
                  setValues(
                    event.target.checked
                      ? [...values, option]
                      : values.filter((value) => value !== option)
                  )
                }
              />
              {option}
            </label>
          ))}
        </div>
        {mutation.isError && (
          <p role='alert' className='mt-2 text-xs text-red-600'>
            {mutation.error.message}
          </p>
        )}
        <Button
          className='mt-3 w-full'
          size='sm'
          disabled={mutation.isPending}
          onClick={() => mutation.mutate()}
        >
          {mutation.isPending ? t('Saving…') : t('Save')}
        </Button>
      </PopoverContent>
    </Popover>
  )
}
