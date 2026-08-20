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
import { cn } from '@/lib/utils'
import { buttonVariants } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

// ============================================================================
// Telegram Bind Dialog Component
// ============================================================================

interface TelegramBindDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  botName: string
}

export function TelegramBindDialog({
  open,
  onOpenChange,
  botName,
}: TelegramBindDialogProps) {
  const { t } = useTranslation()
  const widgetRef = useRef<HTMLDivElement | null>(null)
  const [reloadNonce, setReloadNonce] = useState(0)
  const renderTimerRef = useRef<number | null>(null)
  const renderTimeoutRef = useRef<number | null>(null)
  const [widgetState, setWidgetState] = useState<'loading' | 'ready' | 'error'>(
    'loading'
  )
  const authUrl = useMemo(() => {
    if (typeof window === 'undefined') return ''
    const redirect = `${window.location.origin}/_panel/profile`
    return `${window.location.origin}/api/oauth/telegram/bind?redirect=${encodeURIComponent(redirect)}`
  }, [])

  useEffect(() => {
    if (!open || !botName || !widgetRef.current || !authUrl) return

    const widgetElement = widgetRef.current
    widgetElement.innerHTML = ''
    setWidgetState('loading')

    let settled = false
    const clearTimers = () => {
      if (renderTimerRef.current !== null) {
        window.clearInterval(renderTimerRef.current)
        renderTimerRef.current = null
      }
      if (renderTimeoutRef.current !== null) {
        window.clearTimeout(renderTimeoutRef.current)
        renderTimeoutRef.current = null
      }
    }
    const hasRenderedWidget = () =>
      Array.from(widgetElement.children).some(
        (child) => child.tagName !== 'SCRIPT'
      )
    const markReady = () => {
      if (settled) return
      settled = true
      clearTimers()
      setWidgetState('ready')
    }
    const script = document.createElement('script')
    script.async = true
    script.src = 'https://telegram.org/js/telegram-widget.js?22'
    script.setAttribute('data-telegram-login', botName)
    script.setAttribute('data-size', 'large')
    script.setAttribute('data-auth-url', authUrl)
    script.setAttribute('data-request-access', 'write')
    script.setAttribute('data-radius', '10')
    script.onload = () => {
      if (hasRenderedWidget()) {
        markReady()
        return
      }
      renderTimerRef.current = window.setInterval(() => {
        if (hasRenderedWidget()) {
          markReady()
        }
      }, 100)
    }
    script.onerror = () => {
      if (settled) return
      settled = true
      clearTimers()
      setWidgetState('error')
    }
    widgetElement.appendChild(script)
    renderTimeoutRef.current = window.setTimeout(() => {
      if (settled) return
      settled = true
      clearTimers()
      setWidgetState('error')
    }, 12000)

    return () => {
      script.onload = null
      script.onerror = null
      settled = true
      clearTimers()
      widgetElement.innerHTML = ''
    }
  }, [authUrl, botName, open, reloadNonce])

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
            <div className='flex justify-center' aria-live='polite'>
              <div ref={widgetRef} className='min-h-12' />
            </div>

            <div className='mt-3 flex items-center justify-between gap-3'>
              <p className='text-muted-foreground text-xs'>
                {widgetState === 'loading'
                  ? t('Loading Telegram login widget...')
                  : widgetState === 'error'
                    ? t('Telegram Login Widget failed to load')
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
