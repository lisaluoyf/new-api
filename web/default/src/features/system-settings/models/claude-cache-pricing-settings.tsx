import { useTranslation } from 'react-i18next'
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
import { safeJsonParse } from '../utils/json-parser'

type Props = {
  modelRatio: string
  completionRatio: string
  cacheRatio: string
  createCacheRatio: string
  createCache1hRatio: string
  billingMode: string
  onChange: (
    key: 'CacheRatio' | 'CreateCacheRatio' | 'CreateCache1hRatio',
    value: string
  ) => void
  onSave: () => void
  isSaving: boolean
}

export function ClaudeCachePricingSettings(props: Props) {
  const { t } = useTranslation()
  const parse = (raw: string) =>
    safeJsonParse<Record<string, number>>(raw, { fallback: {}, silent: true })
  const inputs = parse(props.modelRatio)
  const outputs = parse(props.completionRatio)
  const reads = parse(props.cacheRatio)
  const writes = parse(props.createCacheRatio)
  const hours = parse(props.createCache1hRatio)
  const modes = safeJsonParse<Record<string, string>>(props.billingMode, {
    fallback: {},
    silent: true,
  })
  const models = Object.keys(inputs)
    .filter((name) => name.toLowerCase().includes('claude') && inputs[name] > 0)
    .sort()
  const change = (
    name: string,
    key: 'CacheRatio' | 'CreateCacheRatio' | 'CreateCache1hRatio',
    raw: string
  ) => {
    const map = parse(
      key === 'CacheRatio'
        ? props.cacheRatio
        : key === 'CreateCacheRatio'
          ? props.createCacheRatio
          : props.createCache1hRatio
    )
    if (raw === '') delete map[name]
    else {
      const price = Number(raw)
      if (!Number.isFinite(price) || price < 0) return
      map[name] = price / (inputs[name] * 2)
    }
    props.onChange(key, JSON.stringify(map, null, 2))
  }
  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Claude cache prices are in USD per million tokens. Channel and group multipliers apply separately. Empty 1-hour prices inherit 1.6 times the 5-minute price; explicit zero is free.'
        )}
      </p>
      <div className='overflow-x-auto'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Model')}</TableHead>
              <TableHead>{t('Input price')}</TableHead>
              <TableHead>{t('Output price')}</TableHead>
              <TableHead>{t('Cache read price')}</TableHead>
              <TableHead>{t('Cache write price (5 minutes)')}</TableHead>
              <TableHead>{t('Cache write price (1 hour)')}</TableHead>
              <TableHead>{t('Billing mode')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {models.map((name) => {
              const input = inputs[name] * 2
              const fiveMinuteRatio = writes[name] ?? 1.25
              return (
                <TableRow key={name}>
                  <TableCell className='font-mono'>{name}</TableCell>
                  <TableCell>{input}</TableCell>
                  <TableCell>{input * (outputs[name] ?? 5)}</TableCell>
                  <TableCell>
                    <Input
                      aria-label={`${name} ${t('Cache read price')}`}
                      className='min-w-28'
                      type='number'
                      min='0'
                      step='any'
                      value={
                        reads[name] === undefined
                          ? ''
                          : Number((input * reads[name]).toPrecision(12))
                      }
                      onChange={(e) =>
                        change(name, 'CacheRatio', e.target.value)
                      }
                    />
                  </TableCell>
                  <TableCell>
                    <Input
                      aria-label={`${name} ${t('Cache write price (5 minutes)')}`}
                      className='min-w-28'
                      type='number'
                      min='0'
                      step='any'
                      value={
                        writes[name] === undefined
                          ? ''
                          : Number((input * writes[name]).toPrecision(12))
                      }
                      placeholder={String(input * 1.25)}
                      onChange={(e) =>
                        change(name, 'CreateCacheRatio', e.target.value)
                      }
                    />
                  </TableCell>
                  <TableCell>
                    <Input
                      aria-label={`${name} ${t('Cache write price (1 hour)')}`}
                      className='min-w-28'
                      type='number'
                      min='0'
                      step='any'
                      value={
                        hours[name] === undefined
                          ? ''
                          : Number((input * hours[name]).toPrecision(12))
                      }
                      placeholder={String(
                        Number((input * fiveMinuteRatio * 1.6).toPrecision(12))
                      )}
                      onChange={(e) =>
                        change(name, 'CreateCache1hRatio', e.target.value)
                      }
                    />
                  </TableCell>
                  <TableCell>
                    {modes[name] === 'tiered_expr'
                      ? t(
                          'Expression billing: check the expression for user charges'
                        )
                      : t('Token billing')}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
      <Button type='button' disabled={props.isSaving} onClick={props.onSave}>
        {t('Save changes')}
      </Button>
    </div>
  )
}
