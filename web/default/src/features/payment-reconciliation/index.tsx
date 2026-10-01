import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { SectionPageLayout } from '@/components/layout'
import {
  getReconciliation,
  providers,
  queueReconciliation,
  yesterdayBeijing,
  type ReconciliationFilters,
} from './api'

export function PaymentReconciliationPage() {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<ReconciliationFilters>(() => ({
    start_date: yesterdayBeijing(),
    end_date: yesterdayBeijing(),
    provider: 'all',
  }))
  const [filters, setFilters] = useState(draft)
  const [issuesOnly, setIssuesOnly] = useState(false)
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: ['payment-reconciliation', filters, page],
    queryFn: () => getReconciliation(filters, page),
    refetchInterval: 15_000,
    refetchOnWindowFocus: false,
  })
  const mutation = useMutation({
    mutationFn: () => queueReconciliation(filters),
    onSuccess: () => {
      toast.success(t('Reconciliation queued'))
      void query.refetch()
    },
    onError: (error: Error) => toast.error(error.message),
  })
  const data = query.data
  const runs = data?.runs ?? []
  const jobs = data?.jobs ?? []
  const items = data?.items ?? []
  const visibleRuns = issuesOnly
    ? runs.filter((r) => r.difference_count > 0 || r.unverified_count > 0)
    : runs
  const runMap = new Map(runs.map((r) => [r.id, r]))
  const label = (provider: string) => {
    if (provider === 'epay') return t('Epay (Alipay / WeChat Pay)')
    if (provider === 'crypto') return t('On-chain payments')
    if (provider === 'unknown') return t('Unknown payment provider')
    return provider.replace('waffo_pancake', 'Waffo Pancake')
  }
  const status = (value: string) => t(`reconciliation.status.${value}`)
  const formatTime = (value: number) =>
    value
      ? new Date(value * 1000 + 8 * 3600_000)
          .toISOString()
          .slice(0, 19)
          .replace('T', ' ')
      : '—'
  const differences = runs.reduce((sum, r) => sum + r.difference_count, 0)
  const unchecked = runs.reduce((sum, r) => sum + r.unverified_count, 0)
  const running = jobs.some(
    (j) => j.status === 'running' || j.status === 'queued'
  )
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Daily Reconciliation')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t('Daily reconciliation schedule and scope')}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        <p className='text-muted-foreground mb-4 text-sm'>
          {t('Daily reconciliation schedule and scope')}
        </p>
        <div className='mb-5 flex flex-wrap items-end gap-3'>
          <label className='space-y-1 text-sm'>
            {t('Start Date')}
            <Input
              type='date'
              value={draft.start_date}
              onChange={(e) =>
                setDraft({ ...draft, start_date: e.target.value })
              }
            />
          </label>
          <label className='space-y-1 text-sm'>
            {t('End Date')}
            <Input
              type='date'
              value={draft.end_date}
              onChange={(e) => setDraft({ ...draft, end_date: e.target.value })}
            />
          </label>
          <label className='space-y-1 text-sm'>
            {t('Payment Channel')}
            <select
              className='border-input bg-background h-9 w-56 rounded-md border px-3'
              value={draft.provider}
              onChange={(e) => setDraft({ ...draft, provider: e.target.value })}
            >
              <option value='all'>{t('All Channels')}</option>
              {providers.map((p) => (
                <option key={p} value={p}>
                  {label(p)}
                </option>
              ))}
            </select>
          </label>
          <Button
            onClick={() => {
              setPage(1)
              setFilters({ ...draft })
            }}
            disabled={!draft.start_date || !draft.end_date}
          >
            {t('Search')}
          </Button>
          <Button
            variant='outline'
            onClick={() => void query.refetch()}
            disabled={query.isFetching}
          >
            {t('Refresh')}
          </Button>
          <Button
            variant='outline'
            onClick={() => mutation.mutate()}
            disabled={mutation.isPending || running}
          >
            {t('Run Reconciliation')}
          </Button>
          <label className='flex items-center gap-2 text-sm'>
            <input
              type='checkbox'
              checked={issuesOnly}
              onChange={(e) => setIssuesOnly(e.target.checked)}
            />
            {t('Show channels with issues only')}
          </label>
        </div>
        <Alert className='mb-4'>
          <AlertDescription>
            {t('Reconciliation coverage notice')}
          </AlertDescription>
        </Alert>
        {query.isError && (
          <Alert variant='destructive' className='mb-4'>
            <AlertDescription>{query.error.message}</AlertDescription>
          </Alert>
        )}
        {(differences > 0 || unchecked > 0) && (
          <Alert variant='destructive' className='mb-4'>
            <AlertDescription>
              {t('Reconciliation Issues')}: {t('Payment Differences')}{' '}
              {differences} · {t('Awaiting Verification')} {unchecked}
            </AlertDescription>
          </Alert>
        )}
        <div className='mb-5 grid grid-cols-2 gap-3 lg:grid-cols-4'>
          {[
            {
              label: t('Orders Checked'),
              value: runs.reduce((sum, r) => sum + r.checked_count, 0),
            },
            {
              label: t('Local Successful Payments'),
              value: runs.reduce((sum, r) => sum + r.local_paid_count, 0),
            },
            { label: t('Payment Differences'), value: differences },
            { label: t('Awaiting Verification'), value: unchecked },
          ].map((card) => (
            <div key={card.label} className='rounded-lg border p-4'>
              <p className='text-muted-foreground text-sm'>{card.label}</p>
              <p className='mt-2 text-2xl font-semibold tabular-nums'>
                {card.value}
              </p>
            </div>
          ))}
        </div>
        {running && (
          <p className='text-muted-foreground mb-3 text-sm'>
            {t('Reconciliation is running; results refresh automatically')}
          </p>
        )}
        {!query.isLoading && jobs.length === 0 && (
          <p className='text-muted-foreground mb-3 text-sm'>
            {t('No reconciliation yet; run it for the selected dates')}
          </p>
        )}
        <div className='overflow-auto rounded-lg border'>
          <Table>
            <TableHeader>
              <TableRow>
                {[
                  'Date',
                  'Payment Channel',
                  'Orders Checked',
                  'Local Successful Payments',
                  'Official Successful Payments',
                  'Payment Amounts',
                  'Reconciliation Result',
                  'Last updated:',
                ].map((key) => (
                  <TableHead key={key}>{t(key)}</TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {visibleRuns.map((run) => (
                <TableRow key={run.id}>
                  <TableCell>{run.day}</TableCell>
                  <TableCell>{label(run.provider)}</TableCell>
                  <TableCell>{run.checked_count}</TableCell>
                  <TableCell>{run.local_paid_count}</TableCell>
                  <TableCell>{run.official_paid_count}</TableCell>
                  <TableCell className='min-w-60'>
                    {run.totals.map((total) => (
                      <p key={total.currency} className='text-xs tabular-nums'>
                        {total.currency} · {t('Local')}: {total.local_amount} ·{' '}
                        {t('Official')}: {total.official_amount} ·{' '}
                        {t('Difference')}: {total.difference}
                      </p>
                    ))}
                    {run.totals.length === 0 && '—'}
                  </TableCell>
                  <TableCell>
                    <Badge
                      variant={
                        run.difference_count || run.unverified_count
                          ? 'destructive'
                          : 'outline'
                      }
                    >
                      {run.checked_count === 0 && run.status === 'matched'
                        ? t('No payment orders')
                        : status(run.status)}
                    </Badge>
                    <p className='text-muted-foreground mt-1 text-xs'>
                      {t(run.coverage === 'bidirectional_official_statement' ? 'Bidirectional statement checked' : 'Bidirectional statement incomplete')}<br />
                      {t('Payment Differences')}: {run.difference_count} ·{' '}
                      {t('Awaiting Verification')}: {run.unverified_count}
                    </p>
                  </TableCell>
                  <TableCell className='text-xs'>
                    {formatTime(run.finished_at)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        <h2 className='mt-7 mb-3 text-base font-semibold'>
          {t('Reconciliation Issues')}
        </h2>
        <div className='overflow-auto rounded-lg border'>
          <Table>
            <TableHeader>
              <TableRow>
                {[
                  'Date',
                  'Payment Channel',
                  'Order Number',
                  'User ID',
                  'Local Status',
                  'Official Status',
                  'Payment Amounts',
                  'Specific Issue',
                ].map((key) => (
                  <TableHead key={key}>{t(key)}</TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((item) => (
                <TableRow key={item.id}>
                  <TableCell>{runMap.get(item.run_id)?.day}</TableCell>
                  <TableCell>
                    {label(runMap.get(item.run_id)?.provider ?? '')}
                  </TableCell>
                  <TableCell className='max-w-80 text-xs break-all whitespace-normal'>
                    <p>{item.trade_no}</p>
                    <p className='text-muted-foreground'>{item.official_id}</p>
                    <p>
                      {t(
                        item.purpose === 'subscription'
                          ? 'Subscription'
                          : 'Wallet'
                      )}
                    </p>
                  </TableCell>
                  <TableCell>{item.user_id}</TableCell>
                  <TableCell>{item.local_status}</TableCell>
                  <TableCell>{item.official_status || '—'}</TableCell>
                  <TableCell className='text-xs'>
                    <p>
                      {t('Local')}: {item.local_amount} {item.currency}
                    </p>
                    <p>
                      {t('Official')}: {item.official_amount || '—'}{' '}
                      {item.official_currency}
                    </p>
                  </TableCell>
                  <TableCell className='min-w-60 text-sm whitespace-normal'>
                    {t(`reconciliation.problem.${item.problem}`)}
                  </TableCell>
                </TableRow>
              ))}
              {items.length === 0 && (
                <TableRow>
                  <TableCell
                    colSpan={8}
                    className='text-muted-foreground py-8 text-center'
                  >
                    {query.isLoading
                      ? t('Loading...')
                      : t('No reconciliation issues recorded')}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        <div className='mt-3 flex items-center justify-end gap-3'>
          <span className='text-muted-foreground text-sm'>
            {t('Reconciliation Issues')}: {data?.items_total ?? 0} · {page}
          </span>
          <Button
            variant='outline'
            disabled={page <= 1}
            onClick={() => setPage(page - 1)}
          >
            {t('Previous')}
          </Button>
          <Button
            variant='outline'
            disabled={!data?.items_truncated}
            onClick={() => setPage(page + 1)}
          >
            {t('Next')}
          </Button>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
