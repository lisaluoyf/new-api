import { useMemo } from 'react'
import type { ControllerRenderProps } from 'react-hook-form'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import {
  Combobox,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
} from '@/components/ui/combobox'
import { FormControl } from '@/components/ui/form'
import { getEnabledModels } from '@/features/channels/api'
import type { UserFormValues } from '../lib'

type UserDiscountModelInputProps = {
  field: ControllerRenderProps<
    UserFormValues,
    `model_discount_rules.${number}.model`
  >
}

export function UserDiscountModelInput(props: UserDiscountModelInputProps) {
  const { value, onChange, onBlur, name, ref: inputRef } = props.field
  const { t } = useTranslation()
  const { data, isPending } = useQuery({
    queryKey: ['user-discount-model-options'],
    queryFn: getEnabledModels,
    staleTime: 60 * 1000,
  })
  const models = useMemo(
    () => [...new Set(data?.data ?? [])].sort((a, b) => a.localeCompare(b)),
    [data]
  )

  return (
    <Combobox
      items={models}
      value={value || null}
      inputValue={value}
      onInputValueChange={onChange}
      onValueChange={(selectedModel) => {
        if (selectedModel !== null) onChange(selectedModel)
      }}
    >
      <FormControl>
        <ComboboxInput
          ref={inputRef}
          name={name}
          onBlur={onBlur}
          placeholder={t('Search models...')}
          className='w-full min-w-0'
        />
      </FormControl>
      <ComboboxContent>
        <ComboboxEmpty>
          {isPending ? t('Loading...') : t('No models found')}
        </ComboboxEmpty>
        <ComboboxList>
          {(model: string) => (
            <ComboboxItem key={model} value={model}>
              {model}
            </ComboboxItem>
          )}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}
