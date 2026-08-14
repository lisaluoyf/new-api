import { useEffect, useState } from 'react'
import { Save, ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import {
  getGPTSubscriptionAccessConfig,
  updateGPTSubscriptionAccessConfig,
} from '../api'

export function GPTSubscriptionAccessCard() {
  const { t } = useTranslation()
  const [enabled, setEnabled] = useState(false)
  const [emails, setEmails] = useState('lisa.luoyf@gmail.com')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    void getGPTSubscriptionAccessConfig().then((result) => {
      if (!result.success || !result.data) return
      setEnabled(result.data.public_enabled)
      setEmails(result.data.whitelist.join('\n'))
    })
  }, [])

  async function save() {
    setSaving(true)
    try {
      const whitelist = emails
        .split(/[\n,]/)
        .map((email) => email.trim().toLowerCase())
        .filter(Boolean)
      const result = await updateGPTSubscriptionAccessConfig({
        public_enabled: enabled,
        whitelist,
      })
      if (!result.success) throw new Error(result.message)
      toast.success(t('Update succeeded'))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className='mb-4 rounded-lg border bg-card p-4'>
      <div className='flex flex-wrap items-start justify-between gap-4'>
        <div className='flex gap-3'>
          <ShieldCheck className='mt-0.5 size-5 text-fuchsia-500' />
          <div>
            <h3 className='text-sm font-semibold'>{t('Free Model Access')}</h3>
            <p className='text-muted-foreground mt-1 text-xs'>
              {t(
                'When public access is off, only these test accounts can view, purchase, renew, or upgrade. Active subscriptions remain usable.'
              )}
            </p>
          </div>
        </div>
        <label className='flex items-center gap-2 text-sm font-medium'>
          <Switch checked={enabled} onCheckedChange={setEnabled} />
          {t('Open to all users')}
        </label>
      </div>
      <div className='mt-4 grid gap-3 sm:grid-cols-[1fr_auto] sm:items-end'>
        <label className='space-y-2 text-xs font-medium'>
          {t('Test account whitelist (one email per line)')}
          <Textarea
            value={emails}
            onChange={(event) => setEmails(event.target.value)}
            rows={3}
            placeholder='lisa.luoyf@gmail.com'
          />
        </label>
        <Button onClick={() => void save()} disabled={saving}>
          <Save className='mr-2 size-4' />
          {saving ? t('Saving...') : t('Save changes')}
        </Button>
      </div>
    </section>
  )
}
