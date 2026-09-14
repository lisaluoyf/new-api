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
import { useMutation } from '@tanstack/react-query'
import { Download, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { downloadTopupInvoice } from '../api'
import type { TopupRecord } from '../types'

export function InvoiceDownloadButton(props: {
  record: TopupRecord
  isAdmin: boolean
}) {
  const { t } = useTranslation()
  const download = useMutation({
    mutationFn: () => downloadTopupInvoice(props.record.id, props.isAdmin),
    onError: () =>
      toast.error(t('Failed to download invoice. Please try again.')),
  })

  if (
    props.record.status !== 'success' ||
    props.record.money <= 0 ||
    props.record.payment_method === 'free'
  ) {
    return null
  }

  return (
    <Button
      type='button'
      variant='outline'
      size='sm'
      className='h-8 whitespace-nowrap'
      disabled={download.isPending}
      aria-label={t('Download invoice for order {{order}}', {
        order: props.record.trade_no || props.record.id,
      })}
      onClick={() => download.mutate()}
    >
      {download.isPending ? (
        <Loader2 className='size-3.5 animate-spin' aria-hidden='true' />
      ) : (
        <Download className='size-3.5' aria-hidden='true' />
      )}
      {t('Download invoice')}
    </Button>
  )
}
