import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { getProviderChannels } from '../api'
import {
  CHANNEL_CATEGORIES,
  availableChannelCount,
  type ChannelCategory,
} from '../lib/channel-selection'

type ChannelPickerProps = {
  id?: string
  model: string
  value: number[]
  onChange: (value: number[]) => void
  categories: ChannelCategory[]
  onCategoriesChange: (value: ChannelCategory[]) => void
  mode: 'include' | 'exclude'
  disabled?: boolean
}

export function ChannelPicker(props: ChannelPickerProps) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const query = useQuery({
    queryKey: ['provider-channels', props.model],
    queryFn: () => getProviderChannels(props.model),
    enabled: Boolean(props.model),
    staleTime: 30_000,
  })
  const channels = query.data ?? []
  const categoryLabel = (category: ChannelCategory) => {
    if (category === 'cloud') return t('Cloud provider')
    if (category === 'official') return t('Official')
    return t('Others')
  }
  const selectionLabels = [
    ...props.categories.map(categoryLabel),
    ...props.value.map((channelID) => `#${channelID}`),
  ]
  const availableCount = availableChannelCount(
    channels,
    props.value,
    props.categories,
    props.mode
  )
  const ids = [
    ...new Set([
      ...channels.map((channel) => channel.channel_id),
      ...props.value,
    ]),
  ]
  const visible = ids.filter((channelID) =>
    String(channelID).includes(search.trim())
  )

  return (
    <div>
      <Popover>
        <PopoverTrigger
          id={props.id}
          aria-label={t('Select channels')}
          render={
            <Button
              type='button'
              variant='outline'
              disabled={props.disabled || !props.model}
              className='h-auto min-h-10 w-full justify-start text-left whitespace-normal'
            />
          }
        >
          {selectionLabels.length
            ? selectionLabels.join(', ')
            : t('Select channels')}
        </PopoverTrigger>
        <PopoverContent className='w-[min(420px,90vw)] p-3' align='start'>
          <Input
            aria-label={t('Search channels')}
            placeholder={t('Search channels')}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
          <fieldset className='mt-3 border-b pb-3' disabled={props.disabled}>
            <legend className='text-muted-foreground mb-2 text-xs'>
              {t('Channel category')}
            </legend>
            <div className='flex flex-wrap gap-x-4 gap-y-2'>
              {CHANNEL_CATEGORIES.map((category) => (
                <label
                  key={category}
                  className='flex cursor-pointer items-center gap-2 text-sm'
                >
                  <Checkbox
                    checked={props.categories.includes(category)}
                    onCheckedChange={(checked) =>
                      props.onCategoriesChange(
                        checked
                          ? [...props.categories, category]
                          : props.categories.filter(
                              (value) => value !== category
                            )
                      )
                    }
                  />
                  {categoryLabel(category)}
                </label>
              ))}
            </div>
          </fieldset>
          <div className='mt-2 max-h-72 space-y-1 overflow-y-auto'>
            {query.isPending && (
              <p className='p-2 text-sm'>{t('Loading...')}</p>
            )}
            {query.isError && (
              <Button
                type='button'
                variant='outline'
                onClick={() => void query.refetch()}
              >
                {t('Retry')}
              </Button>
            )}
            {!query.isPending && !query.isError && !visible.length && (
              <p className='text-muted-foreground p-2 text-sm'>
                {t('No channels available')}
              </p>
            )}
            {visible.map((channelID) => {
              const channel = channels.find(
                (item) => item.channel_id === channelID
              )
              const category = channel?.channel_category ?? 'default'
              const covered = Boolean(
                channel && props.categories.includes(category)
              )
              const discount =
                channel?.user_price != null &&
                channel.official_input_price &&
                channel.official_input_price > 0
                  ? Math.round(
                      (1 - channel.user_price / channel.official_input_price) *
                        100
                    )
                  : null
              return (
                <label
                  key={channelID}
                  className='hover:bg-muted flex cursor-pointer items-start gap-3 rounded-lg p-2'
                >
                  <Checkbox
                    checked={covered || props.value.includes(channelID)}
                    disabled={covered || props.disabled}
                    aria-label={`${t('Channel')} #${channelID}`}
                    onCheckedChange={(checked) =>
                      props.onChange(
                        checked
                          ? [...props.value, channelID]
                          : props.value.filter((value) => value !== channelID)
                      )
                    }
                  />
                  <span className='min-w-0 text-sm'>
                    <span className='font-medium'>
                      {t('Channel')} #{channelID}
                      {channel && category !== 'default' && (
                        <span className='text-muted-foreground ml-2 text-xs'>
                          {categoryLabel(category)}
                        </span>
                      )}
                    </span>
                    {covered && (
                      <span className='text-muted-foreground block text-xs'>
                        {t(
                          'Selected by category — deselect the category to choose channels individually'
                        )}
                      </span>
                    )}
                    <span className='text-muted-foreground block text-xs'>
                      {channel ? (
                        <>
                          {t('Available')}
                          {channel.user_price != null &&
                            ` · $${Number(channel.user_price.toPrecision(6))}/${t(channel.media_pricing?.unit ?? '1M input tokens')}`}
                          {discount != null &&
                            discount > 0 &&
                            ` · ${t('{{discount}}% off', { discount })}`}
                          {channel.client_exclusive === 'codex' &&
                            ` · ${t('Codex-only channel')}`}
                          {channel.client_exclusive === 'claude_code' &&
                            ` · ${t('Claude Code-only channel')}`}
                        </>
                      ) : (
                        t('Unavailable — saved selection retained')
                      )}
                    </span>
                  </span>
                </label>
              )
            })}
          </div>
        </PopoverContent>
      </Popover>
      {query.isSuccess &&
        (props.value.length > 0 || props.categories.length > 0) &&
        availableCount === 1 && (
          <p
            role='status'
            className='mt-2 text-xs text-amber-700 dark:text-amber-400'
          >
            {t(
              'Only 1 channel is currently available. Cross-channel fallback and racing are unavailable.'
            )}
          </p>
        )}
    </div>
  )
}
