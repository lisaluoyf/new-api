import { useFieldArray, useWatch, type Control } from 'react-hook-form'
import { Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormField,
  FormItem,
  FormLabel,
} from '@/components/ui/form'
import { NumericInput } from '@/components/ui/numeric-input'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import type { ChannelFormValues } from '../lib/channel-form'

export function ModelGroupRatiosField(props: {
  control: Control<ChannelFormValues>
  modelOptions: string[]
}) {
  const { t } = useTranslation()
  const { fields, append, remove } = useFieldArray({
    control: props.control,
    name: 'model_group_ratios',
  })
  const rows =
    useWatch({ control: props.control, name: 'model_group_ratios' }) ?? []
  const usedModels = new Set(rows.map((row) => row.model))

  return (
    <fieldset className='min-w-0 space-y-2'>
      <legend className='mb-2 text-sm font-medium'>
        {t('Per-Model Group Ratio')}
      </legend>
      {fields.map((row, index) => (
        <div
          key={row.id}
          className='grid min-w-0 grid-cols-[minmax(0,1fr)_6rem_2.25rem] items-start gap-2'
        >
          <FormField
            control={props.control}
            name={`model_group_ratios.${index}.model`}
            render={({ field, fieldState }) => (
              <FormItem className='min-w-0'>
                <FormLabel className='sr-only'>{t('Select model')}</FormLabel>
                <FormControl>
                  <select
                    {...field}
                    className='border-input bg-background h-9 w-full min-w-0 rounded-md border px-2 text-sm'
                  >
                    <option value=''>{t('Select model')}</option>
                    {field.value &&
                      !props.modelOptions.includes(field.value) && (
                        <option value={field.value}>{field.value}</option>
                      )}
                    {props.modelOptions
                      .filter(
                        (model) =>
                          model === field.value || !usedModels.has(model)
                      )
                      .map((model) => (
                        <option key={model} value={model}>
                          {model}
                        </option>
                      ))}
                  </select>
                </FormControl>
                {fieldState.error && (
                  <p className='text-destructive text-xs'>
                    {t(fieldState.error.message ?? 'Select model')}
                  </p>
                )}
              </FormItem>
            )}
          />
          <FormField
            control={props.control}
            name={`model_group_ratios.${index}.ratio`}
            render={({ field, fieldState }) => (
              <FormItem>
                <FormLabel className='sr-only'>{t('Group Ratio')}</FormLabel>
                <FormControl>
                  <NumericInput
                    min='0'
                    step='0.0001'
                    placeholder='1.0'
                    value={field.value === 0 ? undefined : field.value}
                    onValueChange={(value) => field.onChange(value ?? 0)}
                    onBlur={field.onBlur}
                  />
                </FormControl>
                {fieldState.error && (
                  <p className='text-destructive text-xs'>
                    {t('Group Ratio must be greater than zero')}
                  </p>
                )}
              </FormItem>
            )}
          />
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  type='button'
                  variant='ghost'
                  size='icon'
                  aria-label={t('Remove model Group Ratio')}
                  onClick={() => remove(index)}
                />
              }
            >
              <Trash2 className='text-destructive size-4' />
            </TooltipTrigger>
            <TooltipContent>{t('Remove model Group Ratio')}</TooltipContent>
          </Tooltip>
        </div>
      ))}
      <Button
        type='button'
        variant='outline'
        size='sm'
        onClick={() => append({ model: '', ratio: 0 })}
      >
        <Plus className='mr-1 size-4' />
        {t('Add model Group Ratio')}
      </Button>
    </fieldset>
  )
}
