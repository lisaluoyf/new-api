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
import { useEffect, useMemo, useRef } from 'react'
import { Send } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
  onSuccess: () => void
}

export function TelegramBindDialog({
  open,
  onOpenChange,
  botName,
}: TelegramBindDialogProps) {
  const { t } = useTranslation()
  const widgetRef = useRef<HTMLDivElement | null>(null)
  const authUrl = useMemo(() => {
    if (typeof window === 'undefined') return ''
    const redirect = `${window.location.origin}/_panel/profile`
    return `${window.location.origin}/api/oauth/telegram/bind?redirect=${encodeURIComponent(redirect)}`
  }, [])

  useEffect(() => {
    if (!open || !botName || !widgetRef.current || !authUrl) return

    widgetRef.current.innerHTML = ''
    const script = document.createElement('script')
    script.async = true
    script.src = 'https://telegram.org/js/telegram-widget.js?22'
    script.setAttribute('data-telegram-login', botName)
    script.setAttribute('data-size', 'large')
    script.setAttribute('data-auth-url', authUrl)
    script.setAttribute('data-request-access', 'write')
    script.setAttribute('data-radius', '10')
    widgetRef.current.appendChild(script)

    return () => {
      if (widgetRef.current) {
        widgetRef.current.innerHTML = ''
      }
    }
  }, [authUrl, botName, open])

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
          <Alert>
            <Send className='h-4 w-4' />
            <AlertDescription>
              {t(
                'You will be redirected to Telegram to complete the binding process.'
              )}
            </AlertDescription>
          </Alert>

          <div className='flex flex-col items-center justify-center gap-4 rounded-lg border p-6'>
            <div className='flex h-12 w-12 items-center justify-center rounded-xl bg-blue-100 dark:bg-blue-900'>
              <Send className='h-6 w-6 text-blue-600 dark:text-blue-400' />
            </div>

            <div className='text-center'>
              <p className='text-muted-foreground text-sm'>
                {t('Bot:')}{' '}
                <span className='font-mono font-semibold'>@{botName}</span>
              </p>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t(
                  "After clicking the button, you'll be asked to authorize the bot"
                )}
              </p>
            </div>

            <div
              ref={widgetRef}
              className='flex min-h-12 justify-center'
              aria-live='polite'
            >
              <div className='text-muted-foreground rounded-lg border border-dashed px-6 py-3 text-sm'>
                {t('Telegram Login Widget')}
              </div>
            </div>
          </div>

          <p className='text-muted-foreground text-center text-xs'>
            {t('The binding will complete automatically after authorization')}
          </p>
        </div>
      </DialogContent>
    </Dialog>
  )
}
