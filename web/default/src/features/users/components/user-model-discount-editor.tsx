import { memo, useCallback, useId } from 'react'
import { useFieldArray, type Control } from 'react-hook-form'
import { Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type { UserFormValues } from '../lib'

type UserModelDiscountEditorProps = {
  control: Control<UserFormValues>
}

export const UserModelDiscountEditor = memo(function UserModelDiscountEditor({
  control,
}: UserModelDiscountEditorProps) {
  const { t } = useTranslation()
  const tableId = useId()
  const { fields, append, remove } = useFieldArray({
    control,
    name: 'model_discount_rules',
  })

  const addRow = useCallback(() => {
    append({ model: '', discount: 1 })
  }, [append])

  return (
    <div className='space-y-4'>
      <div>
        <h3 className='text-sm font-medium'>{t('User model discount')}</h3>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t(
            'Extra wallet discount multiplier. 0.9 means the user pays 90% of the current wallet price.'
          )}
        </p>
      </div>

      <div className='space-y-2'>
        <div className='flex items-center justify-between gap-2'>
          <FormLabel htmlFor={tableId}>{t('Model rules')}</FormLabel>
          <Button type='button' variant='outline' size='sm' onClick={addRow}>
            <Plus className='mr-1 h-4 w-4' />
            {t('Add rule')}
          </Button>
        </div>

        {fields.length === 0 ? (
          <p className='text-muted-foreground text-xs'>
            {t('No model-specific discounts configured.')}
          </p>
        ) : (
          <div className='rounded-md border'>
            <Table id={tableId}>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Model')}</TableHead>
                  <TableHead className='w-[140px]'>{t('Discount')}</TableHead>
                  <TableHead className='w-[52px]' />
                </TableRow>
              </TableHeader>
              <TableBody>
                {fields.map((field, index) => (
                  <TableRow key={field.id}>
                    <TableCell>
                      <FormField
                        control={control}
                        name={`model_discount_rules.${index}.model`}
                        render={({ field: modelField }) => (
                          <FormItem className='space-y-0'>
                            <FormControl>
                              <Input
                                {...modelField}
                                placeholder='seedance-2.5'
                              />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    </TableCell>
                    <TableCell>
                      <FormField
                        control={control}
                        name={`model_discount_rules.${index}.discount`}
                        render={({ field: discountField }) => (
                          <FormItem className='space-y-0'>
                            <FormControl>
                              <Input
                                type='number'
                                min={0.000001}
                                max={1}
                                step='any'
                                value={discountField.value ?? ''}
                                onChange={(e) => {
                                  discountField.onChange(Number(e.target.value))
                                }}
                              />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    </TableCell>
                    <TableCell>
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon'
                        onClick={() => remove(index)}
                        aria-label={t('Remove')}
                      >
                        <Trash2 className='h-4 w-4' />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
        <FormDescription>
          {t(
            'Use exact model names. The multiplier applies to wallet billing only.'
          )}
        </FormDescription>
      </div>
    </div>
  )
})
