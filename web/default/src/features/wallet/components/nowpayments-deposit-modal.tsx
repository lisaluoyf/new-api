import { useEffect, useState } from 'react'
import { Copy, Loader2 } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { getNowPaymentsPayment, requestNowPaymentsPayment } from '../api'

interface NowPaymentsDepositModalProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  amount: number
  onSuccess: () => void
  onSettled?: () => void
}

const CURRENCIES = [
  { value: 'usdttrc20', label: 'USDT', network: 'TRON (TRC20)' },
  { value: 'trx', label: 'TRX', network: 'TRON' },
  { value: 'usdtsol', label: 'USDT', network: 'Solana (SPL)' },
  { value: 'sol', label: 'SOL', network: 'Solana' },
]

export function NowPaymentsDepositModal(props: NowPaymentsDepositModalProps) {
  const { t } = useTranslation()
  const [currency, setCurrency] = useState(CURRENCIES[0].value)
  const [payment, setPayment] = useState<Awaited<ReturnType<typeof requestNowPaymentsPayment>>['data']>(undefined)
  const [loading, setLoading] = useState(false)
  const selected = CURRENCIES.find((item) => item.value === currency) ?? CURRENCIES[0]
	const paymentId = payment?.payment_id
	const open = props.open
	const onSettled = props.onSettled
	const onSuccess = props.onSuccess

  useEffect(() => {
		if (!open || !paymentId) return
    const timer = window.setInterval(async () => {
      try {
				const result = await getNowPaymentsPayment(paymentId)
        if (!result.success || !result.data) return
        if (result.data.topup_status === 'success') {
          window.clearInterval(timer)
          toast.success(t('Payment successful'))
					onSettled?.()
					onSuccess()
        }
      } catch {
        // Webhook remains the authoritative settlement path.
      }
    }, 5000)
    return () => window.clearInterval(timer)
	}, [onSettled, onSuccess, open, paymentId, t])

  async function createPayment() {
    setLoading(true)
    try {
      const result = await requestNowPaymentsPayment({ amount: props.amount, pay_currency: currency })
      if (!result.success || !result.data) {
        toast.error(result.message || t('Failed to create payment'))
        return
      }
      setPayment(result.data)
    } catch {
      toast.error(t('Failed to create payment'))
    } finally {
      setLoading(false)
    }
  }

  async function copy(value: string) {
    await navigator.clipboard.writeText(value)
    toast.success(t('Copied'))
  }

  function reset(open: boolean) {
    if (!open) setPayment(undefined)
    props.onOpenChange(open)
  }

  return (
    <Dialog open={props.open} onOpenChange={reset}>
      <DialogContent className='max-w-md'>
        <DialogHeader>
          <DialogTitle>{t('Pay with cryptocurrency')}</DialogTitle>
          <DialogDescription>{t('Send from any exchange or wallet. No wallet connection required.')}</DialogDescription>
        </DialogHeader>
        {!payment ? (
          <div className='space-y-4'>
            <div className='grid grid-cols-2 gap-2'>
              {CURRENCIES.map((item) => (
                <button key={item.value} type='button' onClick={() => setCurrency(item.value)} className={`rounded-lg border px-3 py-3 text-left ${currency === item.value ? 'border-cyan-500 bg-cyan-50' : 'border-border'}`}>
                  <div className='text-sm font-semibold'>{item.label}</div>
                  <div className='text-xs text-muted-foreground'>{item.network}</div>
                </button>
              ))}
            </div>
            <Button className='w-full' onClick={createPayment} disabled={loading}>
              {loading && <Loader2 className='mr-2 size-4 animate-spin' />}
              {t('Create payment address')}
            </Button>
          </div>
        ) : (
          <div className='space-y-4'>
            <div className='flex justify-center'><QRCodeSVG value={payment.pay_address} size={190} /></div>
            <div className='rounded-lg bg-muted p-3 text-center'>
              <div className='text-xs text-muted-foreground'>{selected.label} · {selected.network}</div>
              <div className='mt-1 font-mono text-lg font-semibold'>{payment.pay_amount}</div>
            </div>
            <div className='flex items-center gap-2 rounded-lg border p-2'>
              <span className='min-w-0 flex-1 break-all font-mono text-xs'>{payment.pay_address}</span>
              <Button size='icon' variant='ghost' onClick={() => copy(payment.pay_address)} aria-label={t('Copy address')}><Copy className='size-4' /></Button>
            </div>
            {payment.payin_extra_id && <div className='text-xs text-amber-600'>{t('Memo / payment ID')}: {payment.payin_extra_id}</div>}
            <p className='text-xs text-muted-foreground'>{t('Only send the exact amount to this address on the selected network. The balance is credited automatically after confirmation.')}</p>
            <div className='flex items-center justify-center gap-2 text-sm text-muted-foreground'><Loader2 className='size-4 animate-spin' />{t('Waiting for payment')}</div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
