import { useState } from 'react'
import { useInfiniteQuery } from '@tanstack/react-query'
import { History } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

interface StatusEvent {
  id: number
  action: string
  source: string
  actor_id: number
  reason: string
  created_at: number
}

export function StatusHistory(props: {
  channelID: number
  channelName: string
  model: string
  reason?: string
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const history = useInfiniteQuery({
    queryKey: ['channel-model-status-history', props.channelID, props.model],
    enabled: open,
    initialPageParam: undefined as number | undefined,
    queryFn: async ({ pageParam }) => {
      const { data } = await api.get<{
        data: StatusEvent[]
        has_more: boolean
      }>('/api/admin/channel-data/status-history', {
        params: {
          channel_id: props.channelID,
          model: props.model,
          before_id: pageParam,
        },
      })
      return data
    },
    getNextPageParam: (last) =>
      last.has_more ? last.data.at(-1)?.id : undefined,
    staleTime: 0,
  })
  const events = history.data?.pages.flatMap((page) => page.data) ?? []
  const sources: Record<string, string> = {
    manual: t('Manual operation'),
    health_probe: t('Health probe'),
    recovery_probe: t('Recovery probe'),
    fingerprint_recovery: t('Fingerprint recovery'),
  }
  return (
    <>
      <Button
        variant='ghost'
        size='icon-sm'
        title={t('Model status history')}
        aria-label={t('Model status history')}
        onClick={() => setOpen(true)}
      >
        <History size={14} />
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='max-h-[85dvh] overflow-y-auto sm:max-w-xl'>
          <DialogHeader>
            <DialogTitle>{t('Model status history')}</DialogTitle>
            <DialogDescription className='break-words'>
              {props.channelName} (#{props.channelID}) · {props.model}
            </DialogDescription>
          </DialogHeader>
          {props.reason && (
            <p className='text-sm break-words'>{t(props.reason)}</p>
          )}
          {history.isPending && <p>{t('Loading...')}</p>}
          {history.isError && (
            <Button variant='outline' onClick={() => void history.refetch()}>
              {t('Retry')}
            </Button>
          )}
          {!history.isPending && !history.isError && events.length === 0 && (
            <p className='text-muted-foreground'>
              {t('No recorded status history')}
            </p>
          )}
          <ol className='divide-y'>
            {events.map((event) => (
              <li key={event.id} className='space-y-1 py-3 text-xs'>
                <div className='flex flex-wrap justify-between gap-2'>
                  <strong>
                    {event.action === 'disable' ? t('Disabled') : t('Enabled')}
                  </strong>
                  <time>
                    {new Date(event.created_at * 1000).toLocaleString()}
                  </time>
                </div>
                <div>
                  {sources[event.source] ?? event.source}
                  {event.actor_id > 0 &&
                    ` · ${t('User ID')}: ${event.actor_id}`}
                </div>
                <p className='break-words whitespace-pre-wrap'>
                  {t(event.reason)}
                </p>
              </li>
            ))}
          </ol>
          {history.hasNextPage && (
            <Button
              variant='outline'
              disabled={history.isFetchingNextPage}
              onClick={() => void history.fetchNextPage()}
            >
              {t('Load more history')}
            </Button>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}
