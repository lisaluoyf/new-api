import { useEffect } from 'react'
import { AxiosError } from 'axios'
import { useFieldArray, useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { handleServerError } from '@/lib/handle-server-error'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Combobox } from '@/components/ui/combobox'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Main } from '@/components/layout'
import {
  getProviderSettings,
  saveProviderSettings,
  type ProviderSettingsData,
} from './api'
import { ChannelPicker } from './components/channel-picker'
import {
  createProviderSettingsSchema,
  type ProviderSettingsValues,
} from './lib/schema'

export function ProviderSettings() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['provider-settings'],
    queryFn: getProviderSettings,
  })
  const form = useForm<ProviderSettingsValues>({
    resolver: zodResolver(createProviderSettingsSchema(t)),
    defaultValues: { rules: [] },
  })
  const rows = useFieldArray({ control: form.control, name: 'rules' })
  const watchedRules = form.watch('rules')
  const mutation = useMutation({
    mutationFn: saveProviderSettings,
    onSuccess: (data) => {
      queryClient.setQueryData<ProviderSettingsData>(
        ['provider-settings'],
        (previous) => ({ models: previous?.models ?? [], rules: data.rules })
      )
      form.reset(data)
      void queryClient.invalidateQueries({ queryKey: ['provider-settings'] })
      toast.success(t('Settings updated successfully'))
    },
    onError: (error) => {
      const message =
        error instanceof AxiosError
          ? error.response?.data?.message
          : error.message
      if (typeof message === 'string' && message) toast.error(t(message))
      else handleServerError(error)
    },
  })

  useEffect(() => {
    if (query.data && !form.formState.isDirty)
      form.reset({ rules: query.data.rules })
  }, [query.data, form, form.formState.isDirty])

  const models = [
    ...new Set([
      ...(query.data?.models ?? []),
      ...watchedRules.map((rule) => rule.model).filter(Boolean),
    ]),
  ]
  const disabled = query.isPending || query.isError || mutation.isPending

  return (
    <Main>
      <div className='min-h-0 flex-1 overflow-auto p-3 sm:p-6'>
        <Card className='mx-auto max-w-7xl'>
          <CardHeader>
            <CardTitle>{t('Provider setting')}</CardTitle>
            <CardDescription>
              {t(
                'Automatic routing is recommended. Add model-specific channel restrictions only when needed. These rules apply to all API keys in your account.'
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {query.isError && (
              <Button
                type='button'
                variant='outline'
                onClick={() => void query.refetch()}
              >
                {t('Retry')}
              </Button>
            )}
            {query.isPending && (
              <p className='mb-4 text-sm'>{t('Loading...')}</p>
            )}
            <Form {...form}>
              <form
                onSubmit={form.handleSubmit((values) =>
                  mutation.mutate(values)
                )}
                className='space-y-4'
              >
                {!rows.fields.length && !query.isPending && (
                  <p className='text-muted-foreground rounded-xl border border-dashed p-8 text-center'>
                    {t('No rules. All models use automatic routing.')}
                  </p>
                )}
                {rows.fields.map((row, index) => (
                  <div
                    key={row.id}
                    className='grid items-start gap-4 rounded-xl border p-4 md:grid-cols-[130px_minmax(180px,1fr)_180px_minmax(180px,1fr)_40px]'
                  >
                    <FormField
                      control={form.control}
                      name={`rules.${index}.enabled`}
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Enabled')}</FormLabel>
                          <FormControl>
                            <div className='flex h-10 items-center gap-2'>
                              <Switch
                                aria-label={t('Enabled')}
                                checked={field.value}
                                onCheckedChange={field.onChange}
                                disabled={disabled}
                              />
                              <span className='text-sm'>
                                {field.value ? t('Enabled') : t('Disabled')}
                              </span>
                            </div>
                          </FormControl>
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={form.control}
                      name={`rules.${index}.model`}
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Model')}</FormLabel>
                          <FormControl>
                            <Combobox
                              options={models.map((model) => ({
                                value: model,
                                label: model,
                              }))}
                              value={field.value}
                              onValueChange={(value) => {
                                if (!disabled && value !== field.value) {
                                  field.onChange(value ?? '')
                                  form.setValue(
                                    `rules.${index}.channel_ids`,
                                    [],
                                    { shouldDirty: true }
                                  )
                                }
                              }}
                              placeholder={t('Select a model')}
                              emptyText={t('No matching models')}
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={form.control}
                      name={`rules.${index}.mode`}
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Restriction')}</FormLabel>
                          <Select
                            value={field.value}
                            onValueChange={field.onChange}
                            disabled={disabled}
                          >
                            <FormControl>
                              <SelectTrigger className='h-10 w-full'>
                                <SelectValue />
                              </SelectTrigger>
                            </FormControl>
                            <SelectContent>
                              <SelectItem value='exclude'>
                                {t('Do not use selected channels')}
                              </SelectItem>
                              <SelectItem value='include'>
                                {t('Only use selected channels')}
                              </SelectItem>
                            </SelectContent>
                          </Select>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={form.control}
                      name={`rules.${index}.channel_ids`}
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Channels')}</FormLabel>
                          <FormControl>
                            <ChannelPicker
                              model={watchedRules[index]?.model ?? ''}
                              value={field.value}
                              onChange={field.onChange}
                              disabled={disabled}
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <Button
                      type='button'
                      variant='ghost'
                      size='icon'
                      className='text-destructive mt-5'
                      aria-label={t('Delete')}
                      disabled={disabled}
                      onClick={() => rows.remove(index)}
                    >
                      <Trash2 className='size-4' />
                    </Button>
                  </div>
                ))}
                <div className='flex flex-wrap items-center justify-between gap-3'>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={disabled || rows.fields.length >= 128}
                    onClick={() =>
                      rows.append({
                        enabled: false,
                        model: '',
                        mode: 'exclude',
                        channel_ids: [],
                      })
                    }
                  >
                    <Plus className='mr-2 size-4' />
                    {t('Add rule')}
                  </Button>
                  <div className='flex gap-2'>
                    <Button
                      type='button'
                      variant='outline'
                      disabled={disabled || !form.formState.isDirty}
                      onClick={() =>
                        form.reset({ rules: query.data?.rules ?? [] })
                      }
                    >
                      {t('Reset')}
                    </Button>
                    <Button
                      type='submit'
                      disabled={disabled || !form.formState.isDirty}
                    >
                      {mutation.isPending ? t('Saving...') : t('Save')}
                    </Button>
                  </div>
                </div>
              </form>
            </Form>
          </CardContent>
        </Card>
      </div>
    </Main>
  )
}
