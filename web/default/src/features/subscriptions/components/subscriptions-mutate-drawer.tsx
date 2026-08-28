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
import { useEffect, useMemo, useState } from 'react'
import { useFieldArray, useForm, type Resolver } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import {
  CalendarClock,
  CreditCard,
  Plus,
  RefreshCw,
  Settings2,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { MultiSelect } from '@/components/multi-select'
import { MODEL_TABS } from '@/features/channel-data/constants'
import { createPlan, updatePlan, getGroups } from '../api'
import { getDurationUnitOptions, getResetPeriodOptions } from '../constants'
import {
  getPlanFormSchema,
  PLAN_FORM_DEFAULTS,
  GPT_TRIAL_PRESET,
  planToFormValues,
  formValuesToPlanPayload,
  type PlanFormValues,
} from '../lib'
import type { PlanRecord } from '../types'
import { useSubscriptions } from './subscriptions-provider'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: PlanRecord
}

function parseModelAllowlist(value?: string): string[] {
  return Array.from(
    new Set(
      (value || '')
        .split(',')
        .map((model) => model.trim())
        .filter(Boolean)
    )
  )
}

export function SubscriptionsMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: Props) {
  const { t } = useTranslation()
  const isEdit = !!currentRow?.plan?.id
  const { triggerRefresh } = useSubscriptions()
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [groupOptions, setGroupOptions] = useState<string[]>([])

  const schema = getPlanFormSchema(t)
  const form = useForm<PlanFormValues>({
    resolver: zodResolver(schema) as unknown as Resolver<PlanFormValues>,
    defaultValues: PLAN_FORM_DEFAULTS,
  })
  const codingModels = useFieldArray({
    control: form.control,
    name: 'coding_models',
  })

  useEffect(() => {
    if (open) {
      if (currentRow?.plan) {
        form.reset(planToFormValues(currentRow.plan))
      } else {
        form.reset(PLAN_FORM_DEFAULTS)
      }
      getGroups()
        .then((res) => {
          if (res.success) setGroupOptions(res.data || [])
        })
        .catch(() => {})
    }
  }, [open, currentRow, form])

  const durationUnit = form.watch('duration_unit')
  const resetPeriod = form.watch('quota_reset_period')
  const modelAllowlist = form.watch('model_allowlist')
  const planType = form.watch('plan_type')

  useEffect(() => {
    if (planType !== 'coding_plan') return
    form.setValue('duration_unit', 'day')
    form.setValue('duration_value', 30)
    form.setValue('quota_reset_period', 'never')
    form.setValue('custom_seconds', 0)
    form.setValue('quota_reset_custom_seconds', 0)
  }, [form, planType])

  const modelOptions = useMemo(() => {
    const channelDataModelIds = new Set(MODEL_TABS.map((tab) => tab.modelId))
    const channelDataOptions = MODEL_TABS.map((tab) => ({
      value: tab.modelId,
      label: `${tab.label} (${tab.modelId})`,
    }))
    const selectedModelsMissingFromChannelData = parseModelAllowlist(
      modelAllowlist
    )
      .filter((model) => !channelDataModelIds.has(model))
      .sort((a, b) => a.localeCompare(b))
      .map((model) => ({
        value: model,
        label: `${model}（当前已选，渠道数据中暂无）`,
      }))

    return [...channelDataOptions, ...selectedModelsMissingFromChannelData]
  }, [modelAllowlist])

  const onSubmit = async (values: PlanFormValues) => {
    setIsSubmitting(true)
    try {
      const payload = formValuesToPlanPayload(values)
      if (isEdit && currentRow?.plan?.id) {
        const res = await updatePlan(currentRow.plan.id, payload)
        if (res.success) {
          toast.success(t('Update succeeded'))
          onOpenChange(false)
          triggerRefresh()
        }
      } else {
        const res = await createPlan(payload)
        if (res.success) {
          toast.success(t('Create succeeded'))
          onOpenChange(false)
          triggerRefresh()
        }
      }
    } catch {
      toast.error(t('Request failed'))
    } finally {
      setIsSubmitting(false)
    }
  }

  const durationUnitOpts = getDurationUnitOptions(t)
  const resetPeriodOpts = getResetPeriodOptions(t)
  const gptSettingLabels: Record<
    'tier_level' | 'five_hour_amount' | 'seven_day_amount',
    string
  > = {
    tier_level: '档位等级',
    five_hour_amount: '5 小时官方价额度（USD）',
    seven_day_amount: '7 天官方价额度（USD）',
  }

  return (
    <Sheet
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v)
        if (!v) {
          form.reset()
        }
      }}
    >
      <SheetContent className='flex h-dvh w-full flex-col gap-0 overflow-hidden p-0 sm:max-w-[600px]'>
        <SheetHeader className='border-b px-4 py-3 text-start sm:px-6 sm:py-4'>
          <SheetTitle>
            {isEdit ? t('Update plan info') : t('Create new subscription plan')}
          </SheetTitle>
          <SheetDescription>
            {isEdit
              ? t('Modify existing subscription plan configuration')
              : t(
                  'Fill in the following info to create a new subscription plan'
                )}
          </SheetDescription>
          {!isEdit && (
            <div className='pt-3'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => form.reset(GPT_TRIAL_PRESET)}
              >
                APIMaster GPT Trial Preset
              </Button>
            </div>
          )}
        </SheetHeader>
        <Form {...form}>
          <form
            id='subscription-form'
            onSubmit={form.handleSubmit(onSubmit)}
            className='flex-1 space-y-4 overflow-y-auto px-3 py-3 pb-4 sm:space-y-6 sm:px-4'
          >
            {/* Basic Info */}
            <div className='space-y-4'>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <Settings2 className='h-4 w-4' />
                {t('Basic Info')}
              </h3>

              <FormField
                control={form.control}
                name='title'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Plan Title')}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder={t('e.g. Basic Plan')} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='subtitle'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Plan Subtitle')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder={t('e.g. Suitable for light usage')}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='plan_type'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Plan Type')}</FormLabel>
                    <Select
                      items={[
                        {
                          value: 'standard',
                          label: t('Standard Subscription'),
                        },
                        { value: 'gpt_trial', label: t('GPT Trial') },
                        {
                          value: 'gpt_subscription',
                          label: 'GPT 订阅',
                        },
                        { value: 'coding_plan', label: 'Coding Plan' },
                      ]}
                      onValueChange={field.onChange}
                      value={field.value || 'standard'}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          <SelectItem value='standard'>
                            {t('Standard Subscription')}
                          </SelectItem>
                          <SelectItem value='gpt_trial'>
                            {t('GPT Trial')}
                          </SelectItem>
                          <SelectItem value='gpt_subscription'>
                            GPT 订阅
                          </SelectItem>
                          <SelectItem value='coding_plan'>
                            Coding Plan
                          </SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FormDescription>
                      {t(
                        'GPT Trial plans are issued through the signup sharing flow and GPT requests will consume them automatically first.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='price_amount'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Actual Amount')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          step='0.01'
                          min={0}
                          onChange={(e) =>
                            field.onChange(parseFloat(e.target.value) || 0)
                          }
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='total_amount'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Total Quota')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min={0}
                          disabled={planType === 'coding_plan'}
                          onChange={(e) =>
                            field.onChange(parseFloat(e.target.value) || 0)
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {planType === 'coding_plan'
                          ? '由官方计价额度自动换算'
                          : t('0 means unlimited')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              {form.watch('plan_type') === 'gpt_subscription' ? (
                <div className='space-y-4 rounded-md border border-fuchsia-500/20 bg-fuchsia-500/5 p-3'>
                  <h4 className='text-sm font-medium'>GPT 订阅设置</h4>
                  <div className='grid grid-cols-1 gap-3 sm:grid-cols-3'>
                    {(
                      [
                        'tier_level',
                        'five_hour_amount',
                        'seven_day_amount',
                      ] as const
                    ).map((name) => (
                      <FormField
                        key={name}
                        control={form.control}
                        name={name}
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{gptSettingLabels[name]}</FormLabel>
                            <FormControl>
                              <Input
                                {...field}
                                type='number'
                                min={0}
                                onChange={(e) =>
                                  field.onChange(Number(e.target.value) || 0)
                                }
                              />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    ))}
                  </div>
                  <FormField
                    control={form.control}
                    name='model_allowlist'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>模型白名单</FormLabel>
                        <FormControl>
                          <MultiSelect
                            options={modelOptions}
                            selected={parseModelAllowlist(field.value)}
                            onChange={(models) =>
                              field.onChange(models.join(','))
                            }
                            placeholder='搜索并选择模型'
                          />
                        </FormControl>
                        <FormDescription>
                          模型列表与“渠道数据”页面保持一致；修改后立即生效。
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='card_description'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>卡片文案</FormLabel>
                        <FormControl>
                          <Textarea
                            {...field}
                            rows={3}
                            placeholder='权益之间使用 | 分隔'
                          />
                        </FormControl>
                        <FormDescription>
                          用于补充该档位已经实际开通的特殊服务或权限，权益之间使用
                          | 分隔；不要填写尚未落地的承诺。
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='recommended'
                    render={({ field }) => (
                      <FormItem className='flex flex-row items-center gap-2'>
                        <FormControl>
                          <Switch
                            checked={field.value}
                            onCheckedChange={field.onChange}
                          />
                        </FormControl>
                        <FormLabel className='!mt-0'>推荐套餐</FormLabel>
                      </FormItem>
                    )}
                  />
                </div>
              ) : null}

              {form.watch('plan_type') === 'coding_plan' ? (
                <div className='space-y-4 rounded-md border border-cyan-500/20 bg-cyan-500/5 p-3'>
                  <h4 className='text-sm font-medium'>Coding Plan 设置</h4>
                  <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                    <FormField
                      control={form.control}
                      name='coding_official_amount_usd'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>官方计价额度（USD）</FormLabel>
                          <FormControl>
                            <Input
                              {...field}
                              type='number'
                              min={0.01}
                              step='0.01'
                              onChange={(event) =>
                                field.onChange(Number(event.target.value) || 0)
                              }
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={form.control}
                      name='tier_level'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>套餐等级</FormLabel>
                          <FormControl>
                            <Input
                              {...field}
                              type='number'
                              min={0}
                              step={1}
                              onChange={(event) =>
                                field.onChange(Number(event.target.value) || 0)
                              }
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>

                  <div className='space-y-2'>
                    <div className='flex items-center justify-between gap-3'>
                      <div>
                        <FormLabel>模型和扣费倍率</FormLabel>
                        <p className='text-muted-foreground text-xs'>
                          模型列表和倍率修改后会立即影响已有活动用户。
                        </p>
                      </div>
                      <Button
                        type='button'
                        variant='outline'
                        size='sm'
                        onClick={() =>
                          codingModels.append({
                            model: '',
                            multiplier: '1.000',
                          })
                        }
                      >
                        <Plus className='mr-1 h-4 w-4' />
                        添加模型
                      </Button>
                    </div>
                    <datalist id='coding-plan-models'>
                      {modelOptions.map((option) => (
                        <option key={option.value} value={option.value} />
                      ))}
                    </datalist>
                    <div className='overflow-hidden rounded-md border'>
                      <div className='bg-muted grid grid-cols-[minmax(0,1fr)_120px_44px] gap-2 px-3 py-2 text-xs font-medium'>
                        <span>模型</span>
                        <span>倍率</span>
                        <span className='sr-only'>操作</span>
                      </div>
                      {codingModels.fields.length === 0 ? (
                        <div className='text-muted-foreground px-3 py-5 text-center text-xs'>
                          尚未配置模型
                        </div>
                      ) : (
                        codingModels.fields.map((item, index) => (
                          <div
                            key={item.id}
                            className='grid grid-cols-[minmax(0,1fr)_120px_44px] gap-2 border-t p-2'
                          >
                            <FormField
                              control={form.control}
                              name={`coding_models.${index}.model`}
                              render={({ field }) => (
                                <FormItem>
                                  <FormControl>
                                    <Input
                                      {...field}
                                      list='coding-plan-models'
                                      placeholder='选择或输入模型 ID'
                                    />
                                  </FormControl>
                                  <FormMessage />
                                </FormItem>
                              )}
                            />
                            <FormField
                              control={form.control}
                              name={`coding_models.${index}.multiplier`}
                              render={({ field }) => (
                                <FormItem>
                                  <FormControl>
                                    <Input
                                      {...field}
                                      type='number'
                                      min='0.001'
                                      max='1.000'
                                      step='0.001'
                                      placeholder='0.500'
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
                              title='删除模型'
                              onClick={() => codingModels.remove(index)}
                            >
                              <Trash2 className='h-4 w-4' />
                              <span className='sr-only'>删除模型</span>
                            </Button>
                          </div>
                        ))
                      )}
                    </div>
                    {form.formState.errors.coding_models?.root?.message ? (
                      <p className='text-destructive text-sm font-medium'>
                        {form.formState.errors.coding_models.root.message}
                      </p>
                    ) : null}
                  </div>

                  <FormField
                    control={form.control}
                    name='card_description'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>卡片权益文案</FormLabel>
                        <FormControl>
                          <Textarea
                            {...field}
                            rows={3}
                            placeholder='权益之间使用 | 分隔'
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='recommended'
                    render={({ field }) => (
                      <FormItem className='flex flex-row items-center gap-2'>
                        <FormControl>
                          <Switch
                            checked={field.value}
                            onCheckedChange={field.onChange}
                          />
                        </FormControl>
                        <FormLabel className='!mt-0'>推荐套餐</FormLabel>
                      </FormItem>
                    )}
                  />
                </div>
              ) : null}

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='upgrade_group'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Upgrade Group')}</FormLabel>
                      <Select
                        items={[
                          { value: '__none__', label: t('No Upgrade') },
                          ...groupOptions.map((g) => ({ value: g, label: g })),
                        ]}
                        onValueChange={(v) =>
                          field.onChange(v === '__none__' ? '' : v)
                        }
                        value={field.value || ''}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue placeholder={t('No Upgrade')} />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            <SelectItem value='__none__'>
                              {t('No Upgrade')}
                            </SelectItem>
                            {groupOptions.map((g) => (
                              <SelectItem key={g} value={g}>
                                {g}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='max_purchase_per_user'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Purchase Limit')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min={0}
                          onChange={(e) =>
                            field.onChange(parseInt(e.target.value, 10) || 0)
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {t('0 means unlimited')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='sort_order'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Sort Order')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          onChange={(e) =>
                            field.onChange(parseInt(e.target.value, 10) || 0)
                          }
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='enabled'
                  render={({ field }) => (
                    <FormItem className='flex flex-row items-center gap-2 pt-8'>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                      <FormLabel className='!mt-0'>
                        {t('Enabled Status')}
                      </FormLabel>
                    </FormItem>
                  )}
                />
              </div>
            </div>

            {/* Duration Settings */}
            <div className='space-y-4'>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <CalendarClock className='h-4 w-4' />
                {t('Duration Settings')}
              </h3>

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='duration_unit'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Duration Unit')}</FormLabel>
                      <Select
                        items={[
                          ...durationUnitOpts.map((o) => ({
                            value: o.value,
                            label: o.label,
                          })),
                        ]}
                        onValueChange={field.onChange}
                        value={field.value}
                        disabled={planType === 'coding_plan'}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            {durationUnitOpts.map((o) => (
                              <SelectItem key={o.value} value={o.value}>
                                {o.label}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                {durationUnit === 'custom' ? (
                  <FormField
                    control={form.control}
                    name='custom_seconds'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Custom Seconds')}</FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            type='number'
                            min={1}
                            disabled={planType === 'coding_plan'}
                            onChange={(e) =>
                              field.onChange(parseInt(e.target.value, 10) || 0)
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                ) : (
                  <FormField
                    control={form.control}
                    name='duration_value'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Duration Value')}</FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            type='number'
                            min={1}
                            disabled={planType === 'coding_plan'}
                            onChange={(e) =>
                              field.onChange(parseInt(e.target.value, 10) || 0)
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                )}
              </div>
            </div>

            {/* Quota Reset */}
            <div className='space-y-4'>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <RefreshCw className='h-4 w-4' />
                {t('Quota Reset')}
              </h3>

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='quota_reset_period'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Reset Cycle')}</FormLabel>
                      <Select
                        items={[
                          ...resetPeriodOpts.map((o) => ({
                            value: o.value,
                            label: o.label,
                          })),
                        ]}
                        onValueChange={field.onChange}
                        value={field.value}
                        disabled={planType === 'coding_plan'}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            {resetPeriodOpts.map((o) => (
                              <SelectItem key={o.value} value={o.value}>
                                {o.label}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='quota_reset_custom_seconds'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Custom Seconds')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min={0}
                          disabled={
                            planType === 'coding_plan' ||
                            resetPeriod !== 'custom'
                          }
                          onChange={(e) =>
                            field.onChange(parseInt(e.target.value, 10) || 0)
                          }
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
            </div>

            {/* Payment Config */}
            <div className='space-y-4'>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <CreditCard className='h-4 w-4' />
                {t('Third-party Payment Config')}
              </h3>

              <FormField
                control={form.control}
                name='stripe_price_id'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Stripe Price ID</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder='price_...' />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='creem_product_id'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Creem Product ID</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder='prod_...' />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </form>
        </Form>
        <SheetFooter className='grid grid-cols-2 gap-2 border-t px-4 py-3 sm:flex sm:px-6 sm:py-4'>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button
            form='subscription-form'
            type='submit'
            disabled={isSubmitting}
          >
            {isSubmitting ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
