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
import { useEffect, useMemo, useRef, useState } from 'react'
import { Send } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { buttonVariants } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

interface TelegramBindDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  botName: string
  onSuccess: () => void
}

type TelegramWidgetMessage = {
  event?: string
  width?: number
  height?: number
  init?: boolean
  auth_data?: Record<string, string | number>
}

export function TelegramBindDialog({
  open,
  onOpenChange,
  botName,
  onSuccess,
}: TelegramBindDialogProps) {
  const { t } = useTranslation()
  const iframeRef = useRef<HTMLIFrameElement | null>(null)
  const bindingRef = useRef(false)
  const [reloadNonce, setReloadNonce] = useState(0)
  const [widgetState, setWidgetState] = useState<
    'loading' | 'ready' | 'binding' | 'error'
  >('loading')
  const widgetUrl = useMemo(() => {
    if (typeof window === 'undefined') return ''
    const params = new URLSearchParams({
      origin: window.location.origin,
      return_to: window.location.href,
      size: 'large',
      request_access: 'write',
      radius: '10',
    })
    return `https://oauth.telegram.org/embed/${encodeURIComponent(botName)}?${params.toString()}`
  }, [botName])

  useEffect(() => {
    if (!open || !widgetUrl || !iframeRef.current) return

    const iframe = iframeRef.current
    bindingRef.current = false
    setWidgetState('loading')

    const renderTimeout = window.setTimeout(() => {
      setWidgetState((current) => (current === 'loading' ? 'error' : current))
    }, 12000)

    const bindTelegramAccount = async (
      authData: Record<string, string | number>
    ) => {
      if (bindingRef.current) return
      bindingRef.current = true
      setWidgetState('binding')

      try {
        const params = new URLSearchParams({ format: 'json' })
        for (const [key, value] of Object.entries(authData)) {
          params.set(key, String(value))
        }
        const response = await fetch(
          `/api/oauth/telegram/bind?${params.toString()}`,
          {
            credentials: 'include',
            headers: { Accept: 'application/json' },
          }
        )
        const result = (await response.json()) as {
          success?: boolean
          message?: string
        }
        if (!response.ok || !result.success) {
          throw new Error(result.message || 'Telegram binding failed')
        }

        onSuccess()
        onOpenChange(false)
        toast.success(t('Binding successful!'))
      } catch {
        bindingRef.current = false
        setWidgetState('ready')
        toast.error(t('OAuth failed'))
      }
    }

    const handleMessage = (event: MessageEvent) => {
      if (
        event.origin !== 'https://oauth.telegram.org' ||
        event.source !== iframe.contentWindow
      ) {
        return
      }

      let message: TelegramWidgetMessage
      try {
        message =
          typeof event.data === 'string'
            ? (JSON.parse(event.data) as TelegramWidgetMessage)
            : (event.data as TelegramWidgetMessage)
      } catch {
        return
      }

      if (message.event === 'ready') {
        window.clearTimeout(renderTimeout)
        setWidgetState('ready')
        iframe.contentWindow?.postMessage(
          JSON.stringify({ event: 'focus', has_focus: document.hasFocus() }),
          'https://oauth.telegram.org'
        )
        return
      }
      if (message.event === 'resize') {
        if (message.width) iframe.style.width = `${message.width}px`
        if (message.height) iframe.style.height = `${message.height}px`
        return
      }
      if (
        message.event === 'auth_user' &&
        !message.init &&
        message.auth_data &&
        typeof message.auth_data === 'object'
      ) {
        void bindTelegramAccount(message.auth_data)
      }
    }

    window.addEventListener('message', handleMessage)
    return () => {
      window.clearTimeout(renderTimeout)
      window.removeEventListener('message', handleMessage)
      bindingRef.current = false
    }
  }, [onOpenChange, onSuccess, open, reloadNonce, t, widgetUrl])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>{t('Bind Telegram Account')}</DialogTitle>
          <DialogDescription>
            {t('Click the button below to bind your Telegram account')}
          </DialogDescription>
        </DialogHeader>

        <div className='space-y-4 py-4'>
          <div className='border-border-default bg-background-surface flex items-center gap-3 rounded-2xl border px-4 py-3'>
            <div className='flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-cyan-500/10'>
              <Send className='h-5 w-5 text-cyan-400' />
            </div>
            <div className='min-w-0'>
              <p className='text-text-primary text-sm font-medium'>
                {t('Bot:')}{' '}
                <span className='font-mono font-semibold'>@{botName}</span>
              </p>
              <p className='text-muted-foreground truncate text-xs leading-5'>
                {t(
                  'The binding will complete automatically after authorization'
                )}
              </p>
            </div>
          </div>

          <div className='border-border-default bg-background rounded-2xl border p-4'>
            <div className='flex min-h-10 justify-center' aria-live='polite'>
              {widgetUrl && widgetState !== 'error' && (
                <iframe
                  key={reloadNonce}
                  ref={iframeRef}
                  src={widgetUrl}
                  title={t('Telegram Login Widget')}
                  width='238'
                  height='40'
                  frameBorder='0'
                  scrolling='no'
                  className='border-0 bg-transparent'
                  onError={() => setWidgetState('error')}
                />
              )}
            </div>

            <div className='mt-3 flex items-center justify-between gap-3'>
              <p className='text-muted-foreground text-xs'>
                {widgetState === 'loading' || widgetState === 'binding'
                  ? t('Loading...')
                  : widgetState === 'error'
                    ? t('Failed to load')
                    : t('Click the button below to bind your Telegram account')}
              </p>

              {widgetState === 'error' && (
                <button
                  type='button'
                  onClick={() => {
                    setWidgetState('loading')
                    setReloadNonce((value) => value + 1)
                  }}
                  className={cn(
                    buttonVariants({ variant: 'outline', size: 'sm' }),
                    'h-8 px-3 text-xs'
                  )}
                >
                  {t('Retry')}
                </button>
              )}
            </div>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
