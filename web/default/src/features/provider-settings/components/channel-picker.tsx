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

type ChannelPickerProps = {
  id?: string
  model: string
  value: number[]
  onChange: (value: number[]) => void
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
        {props.value.length
          ? props.value.map((channelID) => `#${channelID}`).join(', ')
          : t('Select channels')}
      </PopoverTrigger>
      <PopoverContent className='w-[min(420px,90vw)] p-3' align='start'>
        <Input
          aria-label={t('Search channels')}
          placeholder={t('Search channels')}
          value={search}
          onChange={(event) => setSearch(event.target.value)}
        />
        <div className='mt-2 max-h-72 space-y-1 overflow-y-auto'>
          {query.isPending && <p className='p-2 text-sm'>{t('Loading...')}</p>}
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
                  checked={props.value.includes(channelID)}
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
                  </span>
                  <span className='text-muted-foreground block text-xs'>
                    {channel ? (
                      <>
                        {t('Available')}
                        {channel.user_price != null &&
                          ` · $${Number(channel.user_price.toPrecision(6))}/${channel.media_pricing?.unit ?? '1M input tokens'}`}
                        {discount != null &&
                          discount > 0 &&
                          ` · ${discount}% ${t('off')}`}
                        {channel.client_exclusive &&
                          ` · ${channel.client_exclusive}`}
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
  )
}
