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
import { useState, useEffect, useCallback, useMemo } from 'react'
import { Settings2, RefreshCw, AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Switch } from '@/components/ui/switch'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
  TooltipProvider,
} from '@/components/ui/tooltip'
import { GroupBadge } from '@/components/group-badge'
import { SectionPageLayout } from '@/components/layout'
import { StatusBadge } from '@/components/status-badge'
import { parseGroupsList } from '@/features/channels/lib'
import { MODEL_TABS } from './constants'

// ── Types ─────────────────────────────────────────────────────────────────────

interface TopKItem {
  label: string
  score: number
  rank?: number
}

interface DetectPoint {
  status: string // 'pass' / 'suspicious' / 'notcomplete'
  detect_time: number // unix seconds
  note?: string
  group_name?: string // key_group at time of detection
  fingerprint_model_version?: string // e.g. apimaster_fingerprint_cccli_v0.1
  top5?: TopKItem[]
  top1_score_raw?: number // raw top1 score before boost; non-zero only when boost was applied
}

interface ModelDataItem {
  channel_id: number
  channel_name: string
  upstream_model?: string
  priority: number
  group: string
  key_group: string
  client_exclusive?: string // '' | codex | claude_code
  // null when upstream /api/pricing returned 401/404 or cookie-only auth —
  // we have no idea how much this channel costs. UI renders these as "—".
  model_price: number | null // 渠道原价 = input_price / group_ratio (base price the channel claims)
  official_input_price?: number | null // 官方原价 (unified official list price); null = not configured
  official_output_price?: number | null
  base_price_mismatch_pct?: number | null // |渠道原价 − 官方原价| / 官方原价 × 100
  suggested_group_ratio?: number | null // input_price ÷ 官方原价
  group_ratio: number | null // upstream group multiplier
  recharge_rate: number // platform recharge multiplier
  input_price: number | null // model_price × group_ratio
  actual_price: number | null // input_price × recharge_rate (采购价)
  user_price: number | null // actual_price × apimaster_price_ratio (用户最终价格)
  apimaster_price_ratio: number // per-channel markup; 1.0 when unset
  hub_price: number | null // hub.romaapi.com listed price, matched by key_group
  output_price?: number | null
  actual_output_price?: number | null
  actual_output_user_price?: number | null // actual_output_price × apimaster_price_ratio
  cache_price?: number | null // cache-read price before recharge
  actual_cache_price?: number | null // cache_price × recharge_rate
  cache_creation_price?: number | null
  actual_cache_creation_price?: number | null
  media_pricing?: {
    unit: string
    base_variant: string
    official_prices: Record<string, number>
    procurement_prices: Record<string, number>
    billing_prices: Record<string, number>
  }
  fingerprint_history: DetectPoint[]
  uptime_history: DetectPoint[]
  latency_median_ms: number
  latency_p95_ms: number
  latency_cv_pct: number
  status: number // 1 enabled / 2 manual-disabled / 3 auto-disabled
  consecutive_fingerprint_pass: number // recovery counter; meaningful when status=3
  model_enabled: boolean // abilities.enabled for this (channel, model) pair
  pricing_source: string // "api" | "manual" | ""
  status_reason?: string // why auto-disabled; empty when status !== 3
  status_time?: number // unix ts of disable event; 0 if unknown
  base_url?: string
  free_model_config?: FreeModelMemberConfig
  free_model_health?: FreeModelHealth
}

interface FreeModelMemberConfig {
  channel_id: number
  enabled: boolean
  priority: number
  weight: number
  codex_priority: number | null
  codex_weight: number | null
  capabilities: {
    text: boolean
    vision: boolean
    tools: boolean
    codex_tools: boolean | null
    required_tool_call: boolean
    json_object: boolean
    json_schema: boolean
  }
  endpoints: {
    chat_completions: boolean
    responses: boolean
    messages: boolean
  }
  max_context_tokens: number
  timeout_ms: number
  daily_request_limit: number
  daily_request_limit_group: string
}

interface FreeModelHealth {
  status: 'healthy' | 'cooldown' | 'circuit_open' | 'quarantined'
  cooldown_remaining_ms: number
  circuit_remaining_ms: number
  quarantine_remaining_ms: number
  last_failure_reason?: string
  consecutive_failures: number
  recent_success_rate: number
  latency_ms: number
}

interface ProcurementAuditItem {
  channel_id: number
  channel_name: string
  model_name: string
  missing_fields: string[]
}

interface AnalysisState {
  channelName: string
  baseUrl: string
  claimed: string | null
  predicted: string | null
  status: 'idle' | 'loading' | 'done' | 'error'
  text: string
}

interface DetectConfig {
  fingerprint_enabled: boolean
  fingerprint_interval_minutes: number
  uptime_enabled: boolean
  uptime_interval_minutes: number
  next_fingerprint_at?: number // unix sec; 0 means feature off
  next_uptime_at?: number
}

interface ChannelNumericEdit {
  kind: 'route-price'
  channelId: number
  channelName: string
  value: string
}

// ── Constants ─────────────────────────────────────────────────────────────────

const UNIT_OPTIONS = [
  { value: 'minute', toMinutes: (v: number) => v },
  { value: 'hour', toMinutes: (v: number) => v * 60 },
  { value: 'day', toMinutes: (v: number) => v * 1440 },
]

const UNIT_LABEL_KEY: Record<string, string> = {
  minute: 'Minute',
  hour: 'Hour',
  day: 'Day',
}

const DOT_COUNT = 10 // 2 rows × 5 cols
const DOTS_PER_ROW = 5

function minutesToUnit(minutes: number): { value: number; unit: string } {
  if (minutes % 1440 === 0) return { value: minutes / 1440, unit: 'day' }
  if (minutes % 60 === 0) return { value: minutes / 60, unit: 'hour' }
  return { value: minutes, unit: 'minute' }
}

function fmtPrice(price: number | null | undefined): string {
  // null/undefined → 后端没有 pricing 行（上游不暴露 /api/pricing 或 cookie-only auth）
  // 0/负数 → 异常值，同样视为"无价格"
  // 显示破折号而不是 "0"，避免被误认为"免费"渠道。
  if (price == null || price <= 0) return '—'
  return parseFloat(price.toFixed(4)).toString()
}

const MEDIA_VARIANT_ORDER = [
  '1K',
  '2K',
  '4K',
  '480P',
  '480P-input',
  '720P',
  '720P-input',
  '1080P',
  '1080P-input',
  '4K-input',
]

function MediaPriceTable({
  prices,
  unit,
}: {
  prices: Record<string, number>
  unit: string
}) {
  const variants = [
    ...MEDIA_VARIANT_ORDER.filter((variant) => prices[variant] > 0),
    ...Object.keys(prices)
      .filter(
        (variant) =>
          !MEDIA_VARIANT_ORDER.includes(variant) && prices[variant] > 0
      )
      .sort(),
  ]
  return (
    <div className='grid min-w-[220px] grid-cols-[1fr_auto] gap-x-5 gap-y-1 text-xs'>
      {variants.map((variant) => (
        <div key={variant} className='contents'>
          <span className='opacity-70'>{variant}</span>
          <span className='font-mono'>
            ${fmtPrice(prices[variant])}/{unit}
          </span>
        </div>
      ))}
    </div>
  )
}

function ClientExclusiveBadge({ value }: { value?: string }) {
  if (!value) return <span className='text-gray-300'>—</span>
  const styles: Record<string, string> = {
    codex: 'bg-cyan-100 text-cyan-800',
    claude_code: 'bg-violet-100 text-violet-800',
  }
  const labels: Record<string, string> = {
    codex: 'Codex',
    claude_code: 'CC',
  }
  return (
    <span
      className={`inline-flex rounded px-1.5 py-0.5 text-[10px] font-semibold ${styles[value] ?? 'bg-gray-100 text-gray-600'}`}
    >
      {labels[value] ?? value}
    </span>
  )
}

function ChannelGroups({ value }: { value?: string }) {
  const groups = parseGroupsList(value ?? '')
  if (groups.length === 0) {
    return <span className='text-muted-foreground text-xs'>-</span>
  }

  const badges = groups.map((group) => (
    <GroupBadge key={group} group={group} size='sm' />
  ))
  const visibleBadges = badges.slice(0, 2)
  const remaining = badges.length - visibleBadges.length

  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger render={<div />}>
          <div className='flex max-w-full items-center gap-1 overflow-hidden'>
            {visibleBadges}
            {remaining > 0 && (
              <StatusBadge
                label={`+${remaining}`}
                variant='neutral'
                size='sm'
                copyable={false}
                className='flex-shrink-0'
              />
            )}
          </div>
        </TooltipTrigger>
        {remaining > 0 && (
          <TooltipContent
            side='top'
            className='border-border bg-popover max-h-48 max-w-[320px] overflow-y-auto p-2'
          >
            <div className='flex flex-wrap gap-1'>{badges}</div>
          </TooltipContent>
        )}
      </Tooltip>
    </TooltipProvider>
  )
}

// Format unix-sec → "Next 18:42" or "Xs/Xm later" depending on how soon.
// 0 = feature off → empty string.
function fmtNextDetect(
  t: (key: string, opts?: Record<string, unknown>) => string,
  unixSec?: number
): string {
  if (!unixSec) return ''
  const now = Math.floor(Date.now() / 1000)
  const diff = unixSec - now
  if (diff <= 5) return t('Detecting soon')
  if (diff < 60) return t('{{sec}}s later', { sec: diff })
  if (diff < 3600) return t('{{min}}m later', { min: Math.round(diff / 60) })
  const d = new Date(unixSec * 1000)
  const hh = String(d.getHours()).padStart(2, '0')
  const mi = String(d.getMinutes()).padStart(2, '0')
  return t('Next {{time}}', { time: `${hh}:${mi}` })
}

function fmtTime(ts: number): string {
  const d = new Date(ts * 1000)
  const mm = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  const hh = String(d.getHours()).padStart(2, '0')
  const mi = String(d.getMinutes()).padStart(2, '0')
  return `${mm}-${dd} ${hh}:${mi}`
}

const STATUS_LABEL_KEY: Record<string, string> = {
  pass: 'Passed',
  suspicious: 'Suspicious',
  notcomplete: 'Incomplete',
}

const NON_LLM_MODEL_IDS = new Set([
  'gemini-3.1-flash-image',
  'gemini-3.1-flash-image-preview',
  'gpt-image-2',
  'sora-2',
  'sora-2-pro',
  'doubao-seedance-2.0',
  'kling-v3-motion-control',
  'grok-imagine-video-1.5',
  'grok-1.5-video-10s',
  'grok-1.5-video-15s',
  'grok-1.5-video-6s',
])

const VIDEO_MODEL_IDS = new Set([
  'sora-2',
  'sora-2-pro',
  'doubao-seedance-2.0',
  'kling-v3-motion-control',
  'grok-imagine-video-1.5',
  'grok-1.5-video-10s',
  'grok-1.5-video-15s',
  'grok-1.5-video-6s',
])

const IMAGE_MODEL_IDS = new Set([
  'gemini-3.1-flash-image',
  'gemini-3.1-flash-image-preview',
  'gpt-image-2',
])

const PROCUREMENT_FIELD_LABEL_KEY: Record<string, string> = {
  input: 'Input',
  output: 'Output',
  cache_read: 'Cache Read',
  cache_write: 'Cache Write',
}

function hasPositivePrice(price: number | null | undefined): boolean {
  return price != null && price > 0
}

function isLLMModel(modelId: string): boolean {
  if (modelId === 'apimaster-freemodel') return false
  return !NON_LLM_MODEL_IDS.has(modelId)
}

function getPriceUnit(modelId: string): string {
  if (VIDEO_MODEL_IDS.has(modelId)) return '$/s'
  if (IMAGE_MODEL_IDS.has(modelId)) return '$/req'
  return '$/1M'
}

const CHANNEL_DATA_MODEL_IDS = MODEL_TABS.map((tab) => tab.modelId).filter(
  (modelId) => modelId !== 'apimaster-freemodel'
)

function getModelLabel(modelId: string): string {
  return MODEL_TABS.find((tab) => tab.modelId === modelId)?.label ?? modelId
}

function getMissingProcurementFields(
  item: ModelDataItem,
  modelId: string,
  officialHasCacheWrite: boolean
): string[] {
  const missing: string[] = []
  if (!hasPositivePrice(item.actual_price)) missing.push('input')
  if (!isLLMModel(modelId)) return missing

  if (!hasPositivePrice(item.actual_output_price)) missing.push('output')
  if (!hasPositivePrice(item.actual_cache_price)) missing.push('cache_read')
  // cache_write is only required when the unified official price has a
  // cache-creation axis; otherwise upstream 0 means "not applicable".
  if (
    officialHasCacheWrite &&
    !hasPositivePrice(item.actual_cache_creation_price)
  )
    missing.push('cache_write')
  return missing
}

// ── Sub-components ────────────────────────────────────────────────────────────

/**
 * 24-dot grid (2 rows × 12 cols). Newest on the left of the top row.
 * Each dot hover shows: time + status + note (if any) — instant via TooltipProvider delay=0.
 */
function DotGrid({
  history,
  onAnalyze,
}: {
  history: DetectPoint[] | null | undefined
  onAnalyze?: () => void
}) {
  const { t } = useTranslation()
  const safe = history ?? []
  const items: (DetectPoint | null)[] = []
  for (let i = 0; i < DOT_COUNT; i++) {
    items.push(safe[DOT_COUNT - 1 - i] ?? null)
  }

  return (
    <div className='inline-flex flex-col gap-[3px]'>
      {[0, 1].map((row) => (
        <div key={row} className='flex gap-[3px]'>
          {items
            .slice(row * DOTS_PER_ROW, (row + 1) * DOTS_PER_ROW)
            .map((p, i) => {
              let cls = 'bg-gray-200'
              if (p?.status === 'pass') cls = 'bg-emerald-500'
              else if (p?.status === 'suspicious') cls = 'bg-amber-400'
              else if (p?.status === 'notcomplete') cls = 'bg-red-400'

              const dotEl = (
                <div
                  className={`h-[14px] w-[6px] rounded-[2px] ${p ? 'cursor-pointer' : 'cursor-default'} ${cls}`}
                  style={{ opacity: p ? 1 : 0.3 }}
                />
              )

              if (!p) return <div key={i}>{dotEl}</div>

              return (
                <Popover key={i}>
                  <PopoverTrigger render={dotEl} />
                  <PopoverContent
                    side='top'
                    className='w-auto max-w-[420px] min-w-[180px] border-gray-700 bg-gray-900 p-3 text-[12px] text-white'
                  >
                    <div className='flex flex-col gap-1'>
                      <div className='flex items-center justify-between gap-3'>
                        <span className='font-mono opacity-80'>
                          {fmtTime(p.detect_time)}
                        </span>
                        <span className='font-medium'>
                          {t(STATUS_LABEL_KEY[p.status] ?? p.status)}
                        </span>
                      </div>
                      {p.group_name && (
                        <div className='flex items-center gap-1.5 text-[11px] opacity-70'>
                          {t('Group:')}{' '}
                          <span className='font-mono'>{p.group_name}</span>
                          {p.fingerprint_model_version?.includes('cccli') && (
                            <span className='rounded bg-violet-500/30 px-1 py-0.5 text-[10px] font-medium text-violet-300'>
                              cc cli
                            </span>
                          )}
                          {p.fingerprint_model_version?.includes('kiro') && (
                            <span className='rounded bg-amber-500/30 px-1 py-0.5 text-[10px] font-medium text-amber-300'>
                              kiro
                            </span>
                          )}
                        </div>
                      )}
                      {!p.group_name &&
                        (p.fingerprint_model_version?.includes('cccli') ||
                          p.fingerprint_model_version?.includes('kiro')) && (
                          <div className='flex items-center gap-1 text-[11px]'>
                            {p.fingerprint_model_version?.includes('cccli') && (
                              <span className='rounded bg-violet-500/30 px-1 py-0.5 text-[10px] font-medium text-violet-300'>
                                cc cli
                              </span>
                            )}
                            {p.fingerprint_model_version?.includes('kiro') && (
                              <span className='rounded bg-amber-500/30 px-1 py-0.5 text-[10px] font-medium text-amber-300'>
                                kiro
                              </span>
                            )}
                          </div>
                        )}
                      {p.top5 && p.top5.length > 0 && (
                        <div className='mt-0.5 space-y-0.5 border-t border-white/10 pt-1'>
                          <div className='text-[10px] tracking-wide uppercase opacity-50'>
                            Top 5
                          </div>
                          {p.top5.map((topItem, idx) => (
                            <div
                              key={idx}
                              className='flex items-center justify-between gap-3 font-mono text-[11px]'
                            >
                              <span className='truncate'>
                                {idx + 1}. {topItem.label}
                              </span>
                              <span className='tabular-nums opacity-80'>
                                {(topItem.score * 100).toFixed(1)}%
                                {idx === 0 &&
                                  p.top1_score_raw != null &&
                                  p.top1_score_raw > 0 && (
                                    <span className='ml-1 text-[10px] opacity-50'>
                                      {t('(orig: {{pct}}%)', {
                                        pct: (p.top1_score_raw * 100).toFixed(
                                          1
                                        ),
                                      })}
                                    </span>
                                  )}
                              </span>
                            </div>
                          ))}
                        </div>
                      )}
                      {p.note && (
                        <div className='mt-0.5 max-h-[200px] overflow-y-auto border-t border-white/10 pt-1 text-[11px] break-words whitespace-pre-wrap opacity-80'>
                          {p.note}
                        </div>
                      )}
                      {onAnalyze && (
                        <button
                          onClick={onAnalyze}
                          className='mt-1.5 w-full border-t border-white/10 pt-1.5 text-left text-[11px] text-sky-400 transition-colors hover:text-sky-300'
                        >
                          {t('View analysis →')}
                        </button>
                      )}
                    </div>
                  </PopoverContent>
                </Popover>
              )
            })}
        </div>
      ))}
    </div>
  )
}

// ── Interval Settings Dialog ──────────────────────────────────────────────────

function IntervalDialog({
  open,
  onClose,
  initialMinutes,
  onSave,
}: {
  open: boolean
  onClose: () => void
  initialMinutes: number
  onSave: (intervalMinutes: number) => void
}) {
  const { t } = useTranslation()
  const { value: initVal, unit: initUnit } = minutesToUnit(initialMinutes)
  const [value, setValue] = useState(initVal)
  const [unit, setUnit] = useState(initUnit)

  useEffect(() => {
    if (open) {
      const { value: v, unit: u } = minutesToUnit(initialMinutes)
      setValue(v)
      setUnit(u)
    }
  }, [open, initialMinutes])

  function handleSave() {
    const unitOpt = UNIT_OPTIONS.find((o) => o.value === unit)!
    const safeValue = Number.isFinite(value) && value >= 1 ? value : 1
    onSave(unitOpt.toMinutes(safeValue))
    onClose()
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className='max-w-sm'>
        <DialogHeader>
          <DialogTitle>{t('Detection Interval Settings')}</DialogTitle>
        </DialogHeader>
        <div className='space-y-4 py-2'>
          <p className='text-sm text-gray-500'>
            {t('Set the auto-detect interval.')}
          </p>
          <div className='flex items-center gap-2'>
            <Input
              type='number'
              min={1}
              value={value}
              onFocus={(e) => e.target.select()}
              onChange={(e) => {
                const v = e.target.value
                // Allow empty string while editing — user can clear and retype.
                // The Math.max(1, ...) only applies on save (handleSave).
                setValue(v === '' ? (NaN as unknown as number) : Number(v))
              }}
              className='w-24'
            />
            <div className='flex overflow-hidden rounded-md border border-gray-200 text-sm'>
              {UNIT_OPTIONS.map((opt) => (
                <button
                  key={opt.value}
                  onClick={() => setUnit(opt.value)}
                  className={[
                    'px-3 py-1.5 transition-colors',
                    unit === opt.value
                      ? 'bg-gray-900 text-white'
                      : 'text-gray-500 hover:bg-gray-50',
                  ].join(' ')}
                >
                  {t(UNIT_LABEL_KEY[opt.value])}
                </button>
              ))}
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={onClose}>
            {t('Cancel')}
          </Button>
          <Button onClick={handleSave}>{t('Save')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ── Analysis Modal ────────────────────────────────────────────────────────────

function AnalysisModal({
  state,
  onClose,
}: {
  state: AnalysisState
  onClose: () => void
}) {
  const { t } = useTranslation()
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className='w-full max-w-3xl'>
        <DialogHeader>
          <DialogTitle className='text-lg'>
            {t('Channel Detection Analysis — {{name}}', {
              name: state.channelName,
            })}
          </DialogTitle>
          {(state.claimed || state.predicted) && (
            <p className='mt-1 text-xs text-gray-500'>
              {state.claimed && (
                <>
                  {t('Claimed:')}{' '}
                  <span className='text-gray-700'>{state.claimed}</span>
                </>
              )}
              {state.claimed && state.predicted && (
                <span className='mx-1.5 text-gray-300'>·</span>
              )}
              {state.predicted && (
                <>
                  {t('Predicted:')}{' '}
                  <span className='text-gray-700'>{state.predicted}</span>
                </>
              )}
            </p>
          )}
        </DialogHeader>
        <div className='max-h-[75vh] overflow-y-auto py-3'>
          {state.status === 'loading' && (
            <div className='flex items-center gap-2 py-4 text-sm text-gray-500'>
              <RefreshCw className='h-4 w-4 animate-spin' />
              {t('Analyzing…')}
            </div>
          )}
          {state.status === 'error' && (
            <p className='py-2 text-sm text-red-500'>{state.text}</p>
          )}
          {state.status === 'done' && (
            <div className='space-y-1 text-sm leading-relaxed text-gray-700'>
              {state.text.split('\n').map((line, i) => {
                const h2 = line.match(/^##\s+(.*)/)
                const h3 = line.match(/^###\s+(.*)/)
                if (h2)
                  return (
                    <p
                      key={i}
                      className='mt-3 text-base font-semibold text-gray-900'
                    >
                      {h2[1]}
                    </p>
                  )
                if (h3)
                  return (
                    <p key={i} className='mt-2 font-semibold text-gray-800'>
                      {h3[1]}
                    </p>
                  )
                const rendered = line
                  .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
                  .replace(
                    /`(.+?)`/g,
                    "<code class='font-mono text-xs bg-gray-100 px-1 rounded'>$1</code>"
                  )
                return line.trim() === '' ? (
                  <div key={i} className='h-1' />
                ) : (
                  <p key={i} dangerouslySetInnerHTML={{ __html: rendered }} />
                )
              })}
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

// ── Main Page ─────────────────────────────────────────────────────────────────

export function ChannelDataPage() {
  const { t } = useTranslation()
  const [activeModel, setActiveModel] = useState(MODEL_TABS[0].modelId)
  const [data, setData] = useState<ModelDataItem[]>([])
  const [loading, setLoading] = useState(false)
  const [allProcurementAlerts, setAllProcurementAlerts] = useState<
    ProcurementAuditItem[]
  >([])
  const [allProcurementAuditLoaded, setAllProcurementAuditLoaded] =
    useState(false)
  const [allProcurementAuditFailed, setAllProcurementAuditFailed] =
    useState(false)
  // Unified 官方原价 for the active model tab (系统设置 → 模型定价).
  const [official, setOfficial] = useState<{
    input_price: number
    output_price: number
    ok: boolean
    has_cache_write?: boolean
  } | null>(null)
  const [fixingRatio, setFixingRatio] = useState<Record<number, boolean>>({})
  const [config, setConfig] = useState<DetectConfig>({
    fingerprint_enabled: false,
    fingerprint_interval_minutes: 360,
    uptime_enabled: false,
    uptime_interval_minutes: 30,
  })
  const [configLoading, setConfigLoading] = useState(false)
  const [commonAutoReenableEnabled, setCommonAutoReenableEnabled] =
    useState(false)
  const [commonAutoReenableLoading, setCommonAutoReenableLoading] =
    useState(false)
  // True during the initial config fetch for the active model tab.
  // Keeps the toggles visually disabled so users don't see a misleading "OFF" flash.
  const [configFetching, setConfigFetching] = useState(true)
  const [intervalOpen, setIntervalOpen] = useState<
    'fingerprint' | 'uptime' | null
  >(null)
  // refreshing pricing for the current model tab (background task, button shows spinner)
  const [pricingRefreshing, setPricingRefreshing] = useState(false)
  const [pricingRefreshMsg, setPricingRefreshMsg] = useState<string>('')
  const [hubRefreshing, setHubRefreshing] = useState(false)
  const [hubRefreshMsg, setHubRefreshMsg] = useState<string>('')
  // modelId → true if fingerprint_enabled OR uptime_enabled (for tab dot style)
  const [tabDetectEnabled, setTabDetectEnabled] = useState<
    Record<string, boolean>
  >({})
  const [freeSettings, setFreeSettings] = useState({
    cumulative_paid_enabled: true,
    minimum_cumulative_paid_usd: 50,
    active_subscription_enabled: true,
    minimum_subscription_price_usd: 20,
    account_requests_per_minute: 10,
    max_attempts: 3,
    allow_paid_fallback: false,
  })
  const [freeSettingsOpen, setFreeSettingsOpen] = useState(false)
  const [freeMemberEdit, setFreeMemberEdit] = useState<{
    channelName: string
    config: FreeModelMemberConfig
  } | null>(null)
  const [freeMemberSaving, setFreeMemberSaving] = useState(false)
  const [numericEdit, setNumericEdit] = useState<ChannelNumericEdit | null>(
    null
  )
  const [numericEditSaving, setNumericEditSaving] = useState(false)
  const isFreeModel = activeModel === 'apimaster-freemodel'

  // Fetch detect config for all tabs once on mount to show filled/hollow dots
  useEffect(() => {
    const models = MODEL_TABS.map((tab) => tab.modelId).join(',')
    api
      .get('/api/admin/model-detect-config/batch', {
        params: { models },
        skipErrorHandler: true,
      } as Parameters<typeof api.get>[1])
      .then((res) => {
        const batch = (res.data?.data ?? {}) as Record<string, DetectConfig>
        setTabDetectEnabled(
          Object.fromEntries(
            MODEL_TABS.map((tab) => [
              tab.modelId,
              !!(
                batch[tab.modelId]?.fingerprint_enabled ||
                batch[tab.modelId]?.uptime_enabled
              ),
            ])
          )
        )
      })
      .catch(() => {
        setTabDetectEnabled(
          Object.fromEntries(MODEL_TABS.map((tab) => [tab.modelId, false]))
        )
      })
  }, [])

  // Per-channel detecting state: "channelId-modelId" → true while in-flight
  // Keyed by both channel and model so different model tabs don't share detecting state.
  const [detectingChannels, setDetectingChannels] = useState<
    Record<string, boolean>
  >({})
  const [pingingChannels, setPingingChannels] = useState<
    Record<string, boolean>
  >({})
  const [analysis, setAnalysis] = useState<AnalysisState | null>(null)

  const loadAllProcurementAudit = useCallback(() => {
    setAllProcurementAuditLoaded(false)
    setAllProcurementAuditFailed(false)
    api
      .get('/api/admin/channel-data/audit-batch', {
        params: { models: CHANNEL_DATA_MODEL_IDS.join(',') },
        skipErrorHandler: true,
      } as Parameters<typeof api.get>[1])
      .then((res) => {
        if (res.data?.success) {
          setAllProcurementAlerts(res.data.data ?? [])
        }
      })
      .catch(() => {
        setAllProcurementAlerts([])
        setAllProcurementAuditFailed(true)
      })
      .finally(() => setAllProcurementAuditLoaded(true))
  }, [])

  useEffect(() => {
    loadAllProcurementAudit()
  }, [loadAllProcurementAudit])

  // Fetch table data
  useEffect(() => {
    setLoading(true)
    setData([])
    setOfficial(null)
    api
      .get('/api/admin/channel-data', { params: { model: activeModel } })
      .then((res) => {
        if (res.data?.success) {
          const raw: ModelDataItem[] = res.data.data ?? []
          // Sort: enabled (model_enabled+status=1) by user_price asc,
          // then disabled by user_price asc, then no-price last.
          // 与公开市场页一致，按用户最终价格排序。
          const sorted = [...raw].sort((a, b) => {
            if (activeModel === 'apimaster-freemodel') {
              const aEnabled = a.free_model_config?.enabled !== false
              const bEnabled = b.free_model_config?.enabled !== false
              if (aEnabled !== bEnabled) return aEnabled ? -1 : 1
              const priorityDiff =
                (b.free_model_config?.priority ?? 100) -
                (a.free_model_config?.priority ?? 100)
              if (priorityDiff !== 0) return priorityDiff
              return (
                (b.free_model_config?.weight ?? 100) -
                (a.free_model_config?.weight ?? 100)
              )
            }
            const aOn = a.model_enabled !== false && a.status === 1
            const bOn = b.model_enabled !== false && b.status === 1
            if (aOn !== bOn) return aOn ? -1 : 1
            const aP = a.user_price != null && a.user_price > 0
            const bP = b.user_price != null && b.user_price > 0
            if (aP !== bP) return aP ? -1 : 1
            return (a.user_price ?? Infinity) - (b.user_price ?? Infinity)
          })
          setData(sorted)
          if (res.data.official) setOfficial(res.data.official)
        }
      })
      .finally(() => setLoading(false))
  }, [activeModel])

  useEffect(() => {
    if (!isFreeModel) return
    api.get('/api/admin/free-model/settings').then((res) => {
      if (res.data?.success && res.data.data) setFreeSettings(res.data.data)
    })
  }, [isFreeModel])

  const saveFreeSettings = useCallback(() => {
    api
      .put('/api/admin/free-model/settings', freeSettings)
      .then(() => setFreeSettingsOpen(false))
  }, [freeSettings])

  const saveFreeMember = useCallback(async () => {
    if (!freeMemberEdit) return
    setFreeMemberSaving(true)
    try {
      await api.put(
        `/api/admin/free-model/channels/${freeMemberEdit.config.channel_id}/config`,
        freeMemberEdit.config
      )
      setData((current) =>
        current.map((item) =>
          item.channel_id === freeMemberEdit.config.channel_id
            ? { ...item, free_model_config: freeMemberEdit.config }
            : item
        )
      )
      setFreeMemberEdit(null)
    } finally {
      setFreeMemberSaving(false)
    }
  }, [freeMemberEdit])

  const openNumericEdit = useCallback(
    (kind: ChannelNumericEdit['kind'], item: ModelDataItem) => {
      const current = item.input_price ?? item.actual_price ?? 0
      setNumericEdit({
        kind,
        channelId: item.channel_id,
        channelName: item.channel_name,
        value: String(current),
      })
    },
    []
  )

  const saveNumericEdit = useCallback(async () => {
    if (!numericEdit) return
    const value = Number(numericEdit.value)
    const valid = Number.isFinite(value) && value > 0
    if (!valid) return

    setNumericEditSaving(true)
    try {
      await api.put(
        `/api/admin/free-model/channels/${numericEdit.channelId}/route-price`,
        {
          input_price: value,
        }
      )
      const res = await api.get('/api/admin/channel-data', {
        params: { model: activeModel },
      })
      if (res.data?.success) setData(res.data.data ?? [])
      setNumericEdit(null)
    } finally {
      setNumericEditSaving(false)
    }
  }, [activeModel, numericEdit])

  // Fetch detect config when model changes, then poll every 30s so the
  // "下次 HH:MM" countdown stays fresh as auto-detect ticks fire.
  // The `cancelled` flag prevents stale in-flight responses from a previous tab
  // from overwriting the config after the user has already switched tabs.
  useEffect(() => {
    let cancelled = false
    setConfigFetching(true)

    const fetchCfg = () => {
      api
        .get('/api/admin/model-detect-config', {
          params: { model: activeModel },
          skipErrorHandler: true,
        } as Parameters<typeof api.get>[1])
        .then((res) => {
          if (!cancelled) {
            if (res.data?.success) setConfig(res.data.data)
            setConfigFetching(false) // always clear loading after first response
          }
        })
        .catch(() => {
          if (!cancelled) setConfigFetching(false)
        })
    }
    fetchCfg()
    const t = setInterval(fetchCfg, 30_000)
    return () => {
      cancelled = true
      clearInterval(t)
    }
  }, [activeModel])

  useEffect(() => {
    api
      .get('/api/admin/channel-data/common-auto-reenable', {
        skipErrorHandler: true,
      } as Parameters<typeof api.get>[1])
      .then((res) => {
        if (res.data?.success) {
          setCommonAutoReenableEnabled(!!res.data.data?.enabled)
        }
      })
      .catch(() => {})
  }, [])

  // Trigger upstream /api/pricing re-fetch for every channel that serves the
  // current model tab. Fire-and-forget on the backend — wait ~6s then reload
  // the table so freshly upserted channel_model_pricings rows show up.
  const refreshPricing = useCallback(() => {
    if (pricingRefreshing) return
    setPricingRefreshing(true)
    setPricingRefreshMsg('')
    api
      .post('/api/admin/channel-data/refresh-pricing', {})
      .then((res) => {
        const n = res.data?.count ?? 0
        setPricingRefreshMsg(
          t('Triggered refresh for {{n}} channel(s)…', { n })
        )
      })
      .catch(() => setPricingRefreshMsg(t('Refresh failed')))
      .finally(() => {
        // wait for background goroutines to land in DB, then reload table
        setTimeout(() => {
          api
            .get('/api/admin/channel-data', { params: { model: activeModel } })
            .then((res) => {
              if (res.data?.success) setData(res.data.data ?? [])
            })
            .finally(() => {
              loadAllProcurementAudit()
              setPricingRefreshing(false)
              setPricingRefreshMsg('')
            })
        }, 6000)
      })
  }, [activeModel, loadAllProcurementAudit, pricingRefreshing])

  // Re-fetch hub.romaapi.com aggregator pricing (clears the backend TTL cache),
  // then reload the table so the HUB 价格 column shows fresh values.
  const refreshHubPrice = useCallback(() => {
    if (hubRefreshing) return
    setHubRefreshing(true)
    setHubRefreshMsg('')
    api
      .post('/api/admin/channel-data/refresh-hub-price')
      .then((res) => {
        const n = res.data?.count ?? 0
        setHubRefreshMsg(t('Refreshed {{n}} site(s)', { n }))
      })
      .catch(() => setHubRefreshMsg(t('Refresh failed')))
      .finally(() => {
        api
          .get('/api/admin/channel-data', { params: { model: activeModel } })
          .then((res) => {
            if (res.data?.success) setData(res.data.data ?? [])
          })
          .finally(() => {
            setHubRefreshing(false)
            setHubRefreshMsg('')
          })
      })
  }, [activeModel, hubRefreshing])

  // Rewrite channel_model_pricings.group_ratio so 渠道原价 matches 官方原价 for this
  // row. Display-only — input_price / 采购价 / billing are untouched. Rows sourced
  // from the upstream's own /api/pricing get group_ratio re-written on the next
  // "刷新价格", so the alert reappearing means the upstream genuinely changed its
  // base price.
  const fixGroupRatio = useCallback(
    (channelId: number) => {
      if (fixingRatio[channelId]) return
      setFixingRatio((prev) => ({ ...prev, [channelId]: true }))
      api
        .post('/api/admin/channel-data/fix-group-ratio', {
          channel_id: channelId,
          model: activeModel,
        })
        .then(() => {
          api
            .get('/api/admin/channel-data', { params: { model: activeModel } })
            .then((res) => {
              if (res.data?.success) setData(res.data.data ?? [])
            })
        })
        .finally(() =>
          setFixingRatio((prev) => ({ ...prev, [channelId]: false }))
        )
    },
    [activeModel, fixingRatio]
  )

  const detectNow = useCallback(
    (channelId: number) => {
      const key = `${channelId}-${activeModel}`
      if (detectingChannels[key]) return
      setDetectingChannels((prev) => ({ ...prev, [key]: true }))
      api
        .post('/api/admin/channel-data/detect-now', {
          channel_id: channelId,
          model: activeModel,
        })
        .catch(() => {
          /* fire-and-forget; failure is visible in dot-grid */
        })
        .finally(() => {
          // Detection takes ~5-15s on Flask side; reload after 18s to catch result
          setTimeout(() => {
            api
              .get('/api/admin/channel-data', {
                params: { model: activeModel },
              })
              .then((res) => {
                if (res.data?.success) setData(res.data.data ?? [])
              })
              .finally(() =>
                setDetectingChannels((prev) => ({ ...prev, [key]: false }))
              )
          }, 18000)
        })
    },
    [activeModel, detectingChannels]
  )

  const pingNow = useCallback(
    (channelId: number) => {
      const key = `${channelId}-${activeModel}`
      if (pingingChannels[key]) return
      setPingingChannels((prev) => ({ ...prev, [key]: true }))
      api
        .post('/api/admin/channel-data/ping-now', {
          channel_id: channelId,
          model: activeModel,
        })
        .catch(() => {
          /* fire-and-forget; failure is visible in uptime dot-grid */
        })
        .finally(() => {
          // Uptime probe takes a few seconds; reload after 8s to catch result
          setTimeout(() => {
            api
              .get('/api/admin/channel-data', {
                params: { model: activeModel },
              })
              .then((res) => {
                if (res.data?.success) setData(res.data.data ?? [])
              })
              .finally(() =>
                setPingingChannels((prev) => ({ ...prev, [key]: false }))
              )
          }, 8000)
        })
    },
    [activeModel, pingingChannels]
  )

  const saveConfig = useCallback(
    (patch: Partial<DetectConfig>) => {
      const next = { ...config, ...patch }
      setConfig(next)
      setTabDetectEnabled((prev) => ({
        ...prev,
        [activeModel]: !!(next.fingerprint_enabled || next.uptime_enabled),
      }))
      setConfigLoading(true)
      api
        .post('/api/admin/model-detect-config', {
          model: activeModel,
          fingerprint_enabled: next.fingerprint_enabled,
          fingerprint_interval_minutes: next.fingerprint_interval_minutes,
          uptime_enabled: next.uptime_enabled,
          uptime_interval_minutes: next.uptime_interval_minutes,
        })
        .finally(() => setConfigLoading(false))
    },
    [config, activeModel]
  )

  const saveCommonAutoReenable = useCallback((enabled: boolean) => {
    setCommonAutoReenableEnabled(enabled)
    setCommonAutoReenableLoading(true)
    api
      .post('/api/admin/channel-data/common-auto-reenable', { enabled })
      .catch(() => setCommonAutoReenableEnabled(!enabled))
      .finally(() => setCommonAutoReenableLoading(false))
  }, [])

  const handleAnalyze = useCallback(
    async (item: ModelDataItem) => {
      if (!item.base_url) return
      setAnalysis({
        channelName: item.channel_name,
        baseUrl: item.base_url,
        claimed: null,
        predicted: null,
        status: 'loading',
        text: '',
      })
      try {
        const res = await fetch(
          `/api/channel-analysis?base_url=${encodeURIComponent(item.base_url)}`
        )
        const data = await res.json()
        if (data.error) throw new Error(data.error)
        setAnalysis((prev) =>
          prev
            ? {
                ...prev,
                claimed: data.claimed_model ?? null,
                predicted: data.predicted_top1 ?? null,
                status: 'done',
                text: data.analysis ?? t('(no analysis content)'),
              }
            : null
        )
      } catch (e) {
        setAnalysis((prev) =>
          prev
            ? {
                ...prev,
                status: 'error',
                text: e instanceof Error ? e.message : t('Analysis failed'),
              }
            : null
        )
      }
    },
    [t]
  )

  function fmtInterval(minutes: number) {
    const { value, unit } = minutesToUnit(minutes)
    const label = t(UNIT_LABEL_KEY[unit] ?? unit)
    return t('Every {{value}} {{unit}}', { value, unit: label })
  }

  const procurementAlertSummary = useMemo(() => {
    if (!allProcurementAuditLoaded) return null
    if (allProcurementAuditFailed) {
      return {
        tone: 'danger' as const,
        text: t('Price integrity check failed'),
      }
    }
    if (allProcurementAlerts.length === 0) {
      return {
        tone: 'success' as const,
        text: t('All channel prices complete'),
      }
    }

    const uniqueAlerts = Array.from(
      new Map(
        allProcurementAlerts.map((item) => [
          `${item.model_name}:${item.channel_id}`,
          item,
        ])
      ).values()
    )
    const preview = uniqueAlerts
      .slice(0, 3)
      .map((item) => `${getModelLabel(item.model_name)}, ${item.channel_name}`)
      .join('; ')
    const suffix =
      uniqueAlerts.length > 3
        ? t(' and {{n}} more missing price data', { n: uniqueAlerts.length })
        : t(' missing price data')
    return {
      tone: 'danger' as const,
      text: `${preview}${suffix}`,
    }
  }, [
    allProcurementAlerts,
    allProcurementAuditFailed,
    allProcurementAuditLoaded,
    t,
  ])

  const activePriceUnit = useMemo(
    () => getPriceUnit(activeModel),
    [activeModel]
  )
  const activeModelIsLLM = useMemo(() => isLLMModel(activeModel), [activeModel])

  // Manual enable/disable from the row button. Mutates server state then
  // refetches the table so the new status (1 / 2) shows up. We don't update
  // local state optimistically — toggling racing with auto-detect could leave
  // stale data, easier to round-trip.
  const toggleChannel = useCallback(
    (channelId: number, modelEnabled: boolean) => {
      const action = modelEnabled ? 'disable' : 'enable'
      api
        .post('/api/admin/channel-data/toggle', {
          channel_id: channelId,
          model: activeModel,
          action,
        })
        .then(() => {
          // Refresh table
          api
            .get('/api/admin/channel-data', { params: { model: activeModel } })
            .then((res) => {
              if (res.data?.success) setData(res.data.data ?? [])
            })
        })
    },
    [activeModel]
  )

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Channel Data')}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t('Channel pricing and detection stats by model')}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        {/* Model tabs + toolbar */}
        <div className='mb-5 space-y-3'>
          <div className='-mx-1 overflow-x-auto pb-1'>
            <div className='flex min-w-max flex-wrap items-center gap-1.5 px-1'>
              {MODEL_TABS.map((tab) => {
                const active = tab.modelId === activeModel
                return (
                  <button
                    key={tab.modelId}
                    type='button'
                    onClick={() => setActiveModel(tab.modelId)}
                    className={[
                      'inline-flex shrink-0 items-center gap-1.5 rounded-full border px-3 py-1.5 text-sm font-medium transition-all',
                      active
                        ? 'border-gray-900 bg-gray-900 text-white shadow-sm'
                        : 'border-gray-200 bg-white text-gray-500 hover:border-gray-300 hover:bg-gray-50 hover:text-gray-800',
                    ].join(' ')}
                    style={
                      active
                        ? {
                            boxShadow: `0 0 0 2px white, 0 0 0 4px ${tab.accent}55`,
                          }
                        : undefined
                    }
                  >
                    <span
                      className='size-1.5 shrink-0 rounded-full'
                      style={
                        tabDetectEnabled[tab.modelId]
                          ? {
                              backgroundColor: tab.accent,
                              boxShadow: active
                                ? `0 0 6px ${tab.accent}`
                                : undefined,
                            }
                          : {
                              border: `1.5px solid ${tab.accent}`,
                              backgroundColor: 'transparent',
                            }
                      }
                    />
                    {t(tab.label)}
                  </button>
                )
              })}
            </div>
          </div>

          <div className='flex flex-wrap items-center justify-between gap-3'>
            <div className='flex flex-wrap items-center gap-2'>
              {isFreeModel && (
                <button
                  onClick={() => setFreeSettingsOpen(true)}
                  className='inline-flex items-center gap-1.5 rounded-full border border-orange-200 bg-orange-50 px-3 py-1.5 text-sm font-medium text-orange-700 hover:bg-orange-100'
                >
                  <Settings2 className='h-3.5 w-3.5' />
                  {t('FreeModel Settings')}
                </button>
              )}
              {isFreeModel && (
                <span className='rounded-full bg-emerald-50 px-3 py-1.5 text-sm font-medium text-emerald-700'>
                  {t('User billing: Free')}
                </span>
              )}
              <button
                onClick={refreshPricing}
                disabled={pricingRefreshing}
                className='inline-flex items-center gap-1.5 rounded-full border border-gray-200 px-3 py-1.5 text-sm font-medium text-gray-500 transition-colors hover:border-gray-300 hover:text-gray-800 disabled:cursor-not-allowed disabled:opacity-50'
                title={t(
                  'Upstream /api/pricing per channel → channel_model_pricings'
                )}
              >
                <RefreshCw
                  className={`h-3.5 w-3.5 ${pricingRefreshing ? 'animate-spin' : ''}`}
                />
                {pricingRefreshing
                  ? pricingRefreshMsg || t('Refreshing…')
                  : t('Refresh Price')}
              </button>
              <button
                onClick={refreshHubPrice}
                disabled={hubRefreshing}
                className='inline-flex items-center gap-1.5 rounded-full border border-gray-200 px-3 py-1.5 text-sm font-medium text-gray-500 transition-colors hover:border-gray-300 hover:text-gray-800 disabled:cursor-not-allowed disabled:opacity-50'
                title={t('hub.romaapi.com aggregated price (HUB Price column)')}
              >
                <RefreshCw
                  className={`h-3.5 w-3.5 ${hubRefreshing ? 'animate-spin' : ''}`}
                />
                {hubRefreshing
                  ? hubRefreshMsg || t('Refreshing…')
                  : t('Refresh Hub Price')}
              </button>
              {official?.ok && (
                <span
                  className='inline-flex items-center gap-1 rounded-full bg-gray-50 px-3 py-1.5 text-sm font-medium text-gray-600'
                  title={t(
                    'System Settings → Model Pricing (unified official price)'
                  )}
                >
                  {t('Official Price')}
                  <span className='font-semibold text-gray-900 tabular-nums'>
                    ${official.input_price.toFixed(4)}
                  </span>
                  {official.output_price > 0 && (
                    <span className='text-gray-400 tabular-nums'>
                      / ${official.output_price.toFixed(4)}
                    </span>
                  )}
                </span>
              )}
            </div>

            {/* Auto-detect controls: two rows */}
            <div className='flex flex-col items-end gap-1.5'>
              {/* Model detection */}
              {!isFreeModel && (
                <>
                  <div className='flex items-center gap-2'>
                    <span className='w-16 text-right text-xs text-gray-400'>
                      {t('Model Detect')}
                    </span>
                    <Switch
                      id='fp-detect'
                      checked={config.fingerprint_enabled}
                      disabled={configLoading || configFetching}
                      onCheckedChange={(v) =>
                        saveConfig({ fingerprint_enabled: v })
                      }
                    />
                    <button
                      onClick={() => setIntervalOpen('fingerprint')}
                      disabled={configFetching}
                      className='flex items-center gap-1 rounded-md border border-gray-200 px-2 py-1 text-xs text-gray-400 transition-colors hover:text-gray-600 disabled:cursor-not-allowed disabled:opacity-40'
                    >
                      <Settings2 className='h-3 w-3' />
                      {configFetching
                        ? '…'
                        : fmtInterval(config.fingerprint_interval_minutes)}
                    </button>
                    <span className='min-w-[80px] text-[11px] text-gray-400'>
                      {!configFetching && config.fingerprint_enabled
                        ? fmtNextDetect(t, config.next_fingerprint_at)
                        : ''}
                    </span>
                  </div>
                </>
              )}
              {/* Uptime */}
              <div className='flex items-center gap-2'>
                <span className='w-16 text-right text-xs text-gray-400'>
                  {t('Uptime')}
                </span>
                <Switch
                  id='uptime-detect'
                  checked={config.uptime_enabled}
                  disabled={configLoading || configFetching}
                  onCheckedChange={(v) => saveConfig({ uptime_enabled: v })}
                />
                <button
                  onClick={() => setIntervalOpen('uptime')}
                  disabled={configFetching}
                  className='flex items-center gap-1 rounded-md border border-gray-200 px-2 py-1 text-xs text-gray-400 transition-colors hover:text-gray-600 disabled:cursor-not-allowed disabled:opacity-40'
                >
                  <Settings2 className='h-3 w-3' />
                  {configFetching
                    ? '…'
                    : fmtInterval(config.uptime_interval_minutes)}
                </button>
                <span className='min-w-[80px] text-[11px] text-gray-400'>
                  {!configFetching && config.uptime_enabled
                    ? fmtNextDetect(t, config.next_uptime_at)
                    : ''}
                </span>
              </div>
              {/* Ban recovery */}
              <div className='flex items-center gap-2'>
                <span className='w-16 text-right text-xs text-gray-400'>
                  {t('Ban Recovery')}
                </span>
                <Switch
                  id='common-auto-reenable'
                  checked={commonAutoReenableEnabled}
                  disabled={commonAutoReenableLoading}
                  onCheckedChange={(v) => saveCommonAutoReenable(!!v)}
                />
                <span className='min-w-[188px] text-[11px] text-gray-400'>
                  {t('Only probe generic auto-disabled channels')}
                </span>
              </div>
            </div>
          </div>
        </div>

        {/* Enabled count */}
        {!loading &&
          data.length > 0 &&
          (() => {
            const enabledCount = data.filter(
              (it) => it.model_enabled !== false && it.status === 1
            ).length
            return (
              <div className='mb-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-sm text-gray-500'>
                <div>
                  <span className='font-medium text-gray-800'>
                    {enabledCount}
                  </span>{' '}
                  {t('enabled')}
                  <span className='mx-1.5 text-gray-300'>/</span>
                  {t('{{count}} channels', { count: data.length })}
                </div>
                {procurementAlertSummary && (
                  <div
                    className={[
                      'inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium',
                      procurementAlertSummary.tone === 'danger'
                        ? 'bg-red-50 text-red-700'
                        : 'bg-emerald-50 text-emerald-700',
                    ].join(' ')}
                  >
                    {procurementAlertSummary.tone === 'danger' ? (
                      <AlertTriangle className='size-3.5 shrink-0' />
                    ) : (
                      <span className='size-2 shrink-0 rounded-full bg-emerald-500' />
                    )}
                    {procurementAlertSummary.text}
                  </div>
                )}
              </div>
            )
          })()}

        {/* Table */}
        <div className='overflow-x-auto rounded-xl border border-gray-200/80 bg-white'>
          <table className='w-full min-w-max text-sm'>
            <thead>
              <tr className='border-b border-gray-100'>
                <th className='px-3 py-2.5 text-left text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  ID
                </th>
                <th className='w-36 px-3 py-2.5 text-left text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  {t('Site')}
                </th>
                <th className='px-3 py-2.5 text-left text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  {isFreeModel ? t('Upstream Model') : t('Groups')}
                </th>
                <th className='px-3 py-2.5 text-left text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  {t('Site Group')}
                </th>
                <th className='px-3 py-2.5 text-left text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  {t('Client')}
                </th>
                <th className='px-3 py-2.5 text-right text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  {t('Recharge Rate')}
                </th>
                <th className='px-3 py-2.5 text-right text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  {t('Priority')}
                </th>
                <th className='px-2 py-2.5 text-right text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  gratio
                </th>
                <th className='w-16 px-2 py-2 text-right text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  <div className='flex flex-col items-end gap-0.5 leading-tight'>
                    <span>
                      {isFreeModel
                        ? t('Route Price')
                        : t('Channel Original Price')}
                    </span>
                    <span className='font-normal normal-case'>
                      {activePriceUnit}
                    </span>
                  </div>
                </th>
                <th className='w-16 px-2 py-2 text-right text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  <div className='flex flex-col items-end gap-0.5 leading-tight'>
                    <span>{t('Official Price')}</span>
                    <span className='font-normal normal-case'>
                      {activePriceUnit}
                    </span>
                  </div>
                </th>
                <th className='w-20 px-2 py-2 text-right text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  <div className='flex flex-col items-end gap-0.5 leading-tight'>
                    <span>{t('Procurement Price')}</span>
                    <span className='font-normal normal-case'>
                      {activePriceUnit}
                    </span>
                  </div>
                </th>
                <th className='w-16 px-2 py-2 text-right text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  <div className='flex flex-col items-end gap-0.5 leading-tight'>
                    <span>
                      {isFreeModel ? t('Compat Price') : t('User Price')}
                    </span>
                    <span className='font-normal normal-case'>
                      {activePriceUnit}
                    </span>
                  </div>
                </th>
                <th className='w-16 px-2 py-2 text-right text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  <div className='flex flex-col items-end gap-0.5 leading-tight'>
                    <span>{t('HUB Price')}</span>
                    <span className='font-normal normal-case'>
                      {activePriceUnit}
                    </span>
                  </div>
                </th>
                <th className='px-3 py-2.5 text-right text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  {t('Median')}
                </th>
                <th className='px-3 py-2.5 text-right text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  P95
                </th>
                <th className='px-3 py-2.5 text-right text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  {t('Jitter')}
                </th>
                <th className='px-3 py-2.5 text-left text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  {t('Detection Result')}
                </th>
                <th className='px-3 py-2.5 text-left text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  {t('Uptime')}
                </th>
                <th className='px-3 py-2.5 text-center text-xs font-semibold tracking-wide text-gray-400 uppercase'>
                  {t('Actions')}
                </th>
              </tr>
            </thead>
            <tbody className='divide-y divide-gray-50'>
              {loading && (
                <tr>
                  <td
                    colSpan={19}
                    className='px-5 py-12 text-center text-sm text-gray-400'
                  >
                    {t('Loading…')}
                  </td>
                </tr>
              )}
              {!loading && data.length === 0 && (
                <tr>
                  <td
                    colSpan={19}
                    className='px-5 py-12 text-center text-sm text-gray-400'
                  >
                    {t(
                      'No data — please add channels supporting this model in Channel Management'
                    )}
                  </td>
                </tr>
              )}
              {data.map((item) => {
                const missingProcurementFields = getMissingProcurementFields(
                  item,
                  activeModel,
                  official?.has_cache_write ?? false
                )
                const hasProcurementAlert = missingProcurementFields.length > 0
                const isAutoDisabled = item.status === 3
                const isModelEnabled = item.model_enabled !== false // default true if field missing
                const isModelAutoDisabled =
                  !isModelEnabled &&
                  !isAutoDisabled &&
                  Boolean(
                    (item.status_reason && item.status_reason.trim()) ||
                    (item.status_time && item.status_time > 0)
                  )
                const showAutoDisabledBadge =
                  isAutoDisabled || isModelAutoDisabled
                // Effectively enabled = model ability on AND channel not disabled/auto-disabled
                const isEffectivelyEnabled = isModelEnabled && item.status === 1
                // dim when this specific model is disabled on this channel
                const dim = !isModelEnabled ? 'opacity-40' : ''
                // Price divergence alert: actual vs hub > 10%
                const hasBothPrices =
                  item.actual_price != null &&
                  item.actual_price > 0 &&
                  item.hub_price != null &&
                  item.hub_price > 0
                const priceDivergePct = hasBothPrices
                  ? (Math.abs(item.actual_price! - item.hub_price!) /
                      item.hub_price!) *
                    100
                  : 0
                const priceDivergent = priceDivergePct > 10
                const procurementOfficialRatioPct =
                  item.actual_price != null &&
                  item.actual_price > 0 &&
                  item.official_input_price != null &&
                  item.official_input_price > 0
                    ? (item.actual_price / item.official_input_price) * 100
                    : null
                // Tampered-base-price alert: 渠道原价 vs 统一官方原价, computed server-side.
                const baseMismatchPct = item.base_price_mismatch_pct ?? null
                const baseMismatched =
                  baseMismatchPct != null && baseMismatchPct > 5
                return (
                  <tr
                    key={item.channel_id}
                    className='transition-colors hover:bg-gray-50/60'
                  >
                    <td
                      className={`px-3 py-2.5 text-xs text-gray-400 tabular-nums ${dim}`}
                    >
                      {item.channel_id}
                    </td>
                    <td
                      className={`w-36 max-w-[144px] px-3 py-2.5 font-medium text-gray-800 ${dim}`}
                    >
                      <div className='flex flex-col gap-0.5'>
                        <span className='truncate'>{item.channel_name}</span>
                        <div className='flex flex-wrap gap-1'>
                          {showAutoDisabledBadge && (
                            <TooltipProvider delay={0}>
                              <Tooltip>
                                <TooltipTrigger render={<span />}>
                                  <span className='inline-flex cursor-help items-center gap-1 rounded bg-red-100 px-1.5 py-0.5 text-[10px] font-medium text-red-700 dark:bg-red-900/30 dark:text-red-400'>
                                    <AlertTriangle size={10} />
                                    {t('Disabled {{n}}/12', {
                                      n: item.consecutive_fingerprint_pass,
                                    })}
                                  </span>
                                </TooltipTrigger>
                                <TooltipContent className='max-w-xs'>
                                  <div className='space-y-1 text-xs'>
                                    <div className='font-medium text-red-400'>
                                      {isModelAutoDisabled
                                        ? t('Model auto-disabled')
                                        : t('Channel auto-disabled')}
                                    </div>
                                    {item.status_reason && (
                                      <div>
                                        {t('Reason: {{reason}}', {
                                          reason: item.status_reason,
                                        })}
                                      </div>
                                    )}
                                    {item.status_time &&
                                      item.status_time > 0 && (
                                        <div>
                                          {t('Time: {{time}}', {
                                            time: fmtTime(item.status_time),
                                          })}
                                        </div>
                                      )}
                                  </div>
                                </TooltipContent>
                              </Tooltip>
                            </TooltipProvider>
                          )}
                          {!isModelEnabled && !isModelAutoDisabled && (
                            <span className='rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-500'>
                              {t('Disabled')}
                            </span>
                          )}
                        </div>
                      </div>
                    </td>
                    <td className={`px-3 py-2.5 ${dim}`}>
                      {isFreeModel ? (
                        <div className='flex flex-col gap-0.5 text-xs'>
                          <span className='font-mono text-gray-700'>
                            {item.upstream_model || '—'}
                          </span>
                          <span className='text-gray-400'>
                            {t('Mapped upstream model')}
                            {item.free_model_config &&
                              ` · P${item.free_model_config.priority} · W${item.free_model_config.weight}`}
                          </span>
                          {item.free_model_health && (
                            <span
                              className={
                                item.free_model_health.status === 'healthy'
                                  ? 'text-emerald-600'
                                  : 'text-amber-600'
                              }
                            >
                              {item.free_model_health.status} ·{' '}
                              {(
                                item.free_model_health.recent_success_rate * 100
                              ).toFixed(1)}
                              % · {item.free_model_health.latency_ms.toFixed(0)}{' '}
                              ms
                              {item.free_model_health.last_failure_reason
                                ? ` · ${item.free_model_health.last_failure_reason}`
                                : ''}
                            </span>
                          )}
                        </div>
                      ) : (
                        <ChannelGroups value={item.group} />
                      )}
                    </td>
                    <td className={`px-3 py-2.5 text-gray-500 ${dim}`}>
                      {item.key_group || (
                        <span className='text-gray-300'>—</span>
                      )}
                    </td>
                    <td className={`px-3 py-2.5 ${dim}`}>
                      <ClientExclusiveBadge value={item.client_exclusive} />
                    </td>
                    <td
                      className={`px-3 py-2.5 text-right text-xs text-gray-500 tabular-nums ${dim}`}
                    >
                      {item.recharge_rate != null ? (
                        item.recharge_rate.toFixed(4)
                      ) : (
                        <span className='text-gray-300'>—</span>
                      )}
                    </td>
                    <td
                      className={`px-3 py-2.5 text-right text-xs text-gray-500 tabular-nums ${dim}`}
                    >
                      {isFreeModel
                        ? (item.free_model_config?.priority ?? 100)
                        : (item.priority ?? 0)}
                    </td>
                    <td
                      className={`px-2 py-3.5 text-right text-xs text-gray-500 tabular-nums ${dim}`}
                    >
                      {item.group_ratio != null ? (
                        item.group_ratio.toFixed(3)
                      ) : (
                        <span className='text-gray-300'>—</span>
                      )}
                    </td>
                    <td
                      className={`px-2 py-2.5 text-right tabular-nums ${baseMismatched ? 'font-semibold text-red-600' : 'text-gray-600'} ${dim}`}
                    >
                      <TooltipProvider delay={0}>
                        <Tooltip>
                          <TooltipTrigger
                            render={
                              <div className='inline-flex cursor-default items-center justify-end gap-1'>
                                {baseMismatched && (
                                  <span className='rounded bg-red-100 px-1.5 py-0.5 text-[10px] leading-none font-bold text-red-600'>
                                    !{Math.round(baseMismatchPct!)}%
                                  </span>
                                )}
                                {fmtPrice(item.model_price)}
                              </div>
                            }
                          />
                          <TooltipContent>
                            {baseMismatched ? (
                              <div className='flex min-w-[200px] flex-col gap-1.5 text-[12px]'>
                                <div className='font-medium text-red-300'>
                                  {t('This channel changed its own base price')}
                                </div>
                                <div className='flex justify-between gap-4'>
                                  <span className='opacity-70'>
                                    {t('Official Price')}
                                  </span>
                                  <span className='font-mono'>
                                    {fmtPrice(item.official_input_price)}
                                  </span>
                                </div>
                                <div className='flex justify-between gap-4'>
                                  <span className='opacity-70'>
                                    {t('Channel Original Price')}
                                  </span>
                                  <span className='font-mono'>
                                    {fmtPrice(item.model_price)}
                                  </span>
                                </div>
                                <div className='flex justify-between gap-4'>
                                  <span className='opacity-70'>
                                    {t('Suggested gratio')}
                                  </span>
                                  <span className='font-mono'>
                                    {item.suggested_group_ratio?.toFixed(3) ??
                                      '—'}
                                  </span>
                                </div>
                                <button
                                  onClick={() => fixGroupRatio(item.channel_id)}
                                  disabled={!!fixingRatio[item.channel_id]}
                                  className='mt-1 w-full border-t border-white/10 pt-1.5 text-left text-[11px] text-sky-400 transition-colors hover:text-sky-300 disabled:opacity-50'
                                >
                                  {fixingRatio[item.channel_id]
                                    ? t('Fixing…')
                                    : t('Reverse gratio from official price →')}
                                </button>
                                {item.pricing_source === 'api' && (
                                  <div className='text-[10px] opacity-50'>
                                    {t(
                                      'Next "Refresh Price" will revert this alert if upstream base price is unchanged'
                                    )}
                                  </div>
                                )}
                              </div>
                            ) : (
                              <span className='opacity-70'>
                                {t('Matches official price')}
                              </span>
                            )}
                          </TooltipContent>
                        </Tooltip>
                      </TooltipProvider>
                    </td>
                    <td
                      className={`px-2 py-2.5 text-right text-gray-500 tabular-nums ${dim}`}
                    >
                      {item.media_pricing ? (
                        <TooltipProvider delay={0}>
                          <Tooltip>
                            <TooltipTrigger
                              render={
                                <span className='cursor-default border-b border-dotted border-gray-400' />
                              }
                            >
                              {fmtPrice(item.official_input_price)}
                            </TooltipTrigger>
                            <TooltipContent>
                              <MediaPriceTable
                                prices={item.media_pricing.official_prices}
                                unit={item.media_pricing.unit}
                              />
                            </TooltipContent>
                          </Tooltip>
                        </TooltipProvider>
                      ) : (
                        fmtPrice(item.official_input_price)
                      )}
                    </td>
                    <td
                      className={`px-2 py-2.5 text-right font-semibold tabular-nums ${priceDivergent || hasProcurementAlert ? 'text-red-600' : 'text-gray-800'} ${dim}`}
                    >
                      <TooltipProvider delay={0}>
                        <Tooltip>
                          <TooltipTrigger
                            render={
                              <div className='inline-flex cursor-default items-center justify-end gap-1.5'>
                                {item.pricing_source === 'manual' && (
                                  <span className='rounded bg-amber-100 px-1.5 py-0.5 text-[10px] leading-none font-medium text-amber-700'>
                                    {t('Manual')}
                                  </span>
                                )}
                                {item.pricing_source === 'api' && (
                                  <span className='rounded bg-green-100 px-1.5 py-0.5 text-[10px] leading-none font-medium text-green-700'>
                                    pricing
                                  </span>
                                )}
                                {item.pricing_source === 'media' && (
                                  <span className='rounded bg-violet-100 px-1.5 py-0.5 text-[10px] leading-none font-medium text-violet-700'>
                                    8 tiers
                                  </span>
                                )}
                                {item.pricing_source === 'image' && (
                                  <span className='rounded bg-cyan-100 px-1.5 py-0.5 text-[10px] leading-none font-medium text-cyan-700'>
                                    Image
                                  </span>
                                )}
                                {priceDivergent && (
                                  <span className='rounded bg-red-100 px-1.5 py-0.5 text-[10px] leading-none font-bold text-red-600'>
                                    !{Math.round(priceDivergePct)}%
                                  </span>
                                )}
                                {hasProcurementAlert && (
                                  <span className='inline-flex items-center gap-1 rounded bg-red-100 px-1.5 py-0.5 text-[10px] leading-none font-bold text-red-600'>
                                    <AlertTriangle size={10} />
                                    {t('Missing Price')}
                                  </span>
                                )}
                                {procurementOfficialRatioPct != null && (
                                  <span
                                    className={`rounded px-1.5 py-0.5 text-[10px] leading-none font-bold ${
                                      procurementOfficialRatioPct <= 100
                                        ? 'bg-emerald-100 text-emerald-700'
                                        : 'bg-orange-100 text-orange-700'
                                    }`}
                                    title={t('Procurement / official price')}
                                  >
                                    {procurementOfficialRatioPct.toFixed(1)}%
                                  </span>
                                )}
                                {fmtPrice(item.actual_price)}
                              </div>
                            }
                          />
                          <TooltipContent>
                            {item.media_pricing ? (
                              <MediaPriceTable
                                prices={item.media_pricing.procurement_prices}
                                unit={item.media_pricing.unit}
                              />
                            ) : item.actual_price != null &&
                              item.actual_price > 0 ? (
                              <div className='flex min-w-[160px] flex-col gap-1 text-[12px]'>
                                {hasProcurementAlert && (
                                  <div className='mb-1 border-b border-white/10 pb-1 text-red-300'>
                                    {t(
                                      'Incomplete procurement price: missing {{fields}}',
                                      {
                                        fields: missingProcurementFields
                                          .map((field) =>
                                            t(
                                              PROCUREMENT_FIELD_LABEL_KEY[
                                                field
                                              ] ?? field
                                            )
                                          )
                                          .join(' / '),
                                      }
                                    )}
                                  </div>
                                )}
                                <div className='flex justify-between gap-4'>
                                  <span className='opacity-70'>
                                    {t('Input')}
                                  </span>
                                  <span className='font-mono'>
                                    {fmtPrice(item.actual_price)}
                                  </span>
                                </div>
                                {activeModelIsLLM && (
                                  <>
                                    <div className='flex justify-between gap-4'>
                                      <span className='opacity-70'>
                                        {t('Output')}
                                      </span>
                                      <span className='font-mono'>
                                        {fmtPrice(item.actual_output_price)}
                                      </span>
                                    </div>
                                    <div className='flex justify-between gap-4'>
                                      <span className='opacity-70'>
                                        {t('Cache Read')}
                                      </span>
                                      <span className='font-mono'>
                                        {fmtPrice(item.actual_cache_price)}
                                      </span>
                                    </div>
                                    <div className='flex justify-between gap-4'>
                                      <span className='opacity-70'>
                                        {t('Cache Write')}
                                      </span>
                                      <span className='font-mono'>
                                        {fmtPrice(
                                          item.actual_cache_creation_price
                                        )}
                                      </span>
                                    </div>
                                  </>
                                )}
                                {priceDivergent && (
                                  <div className='mt-0.5 border-t border-white/10 pt-1 text-red-300'>
                                    {t(
                                      'Deviates from Hub {{pct}}% (Hub input: {{price}})',
                                      {
                                        pct: Math.round(priceDivergePct),
                                        price: fmtPrice(item.hub_price),
                                      }
                                    )}
                                  </div>
                                )}
                              </div>
                            ) : (
                              <div className='flex min-w-[160px] flex-col gap-1 text-[12px]'>
                                {hasProcurementAlert && (
                                  <div className='text-red-300'>
                                    {t(
                                      'Incomplete procurement price: missing {{fields}}',
                                      {
                                        fields: missingProcurementFields
                                          .map((field) =>
                                            t(
                                              PROCUREMENT_FIELD_LABEL_KEY[
                                                field
                                              ] ?? field
                                            )
                                          )
                                          .join(' / '),
                                      }
                                    )}
                                  </div>
                                )}
                                <span className='opacity-70'>
                                  {t('No price data yet')}
                                </span>
                              </div>
                            )}
                          </TooltipContent>
                        </Tooltip>
                      </TooltipProvider>
                    </td>
                    <td
                      className={`px-2 py-2.5 text-right font-semibold text-emerald-700 tabular-nums ${dim}`}
                    >
                      {fmtPrice(item.user_price)}
                      {!item.media_pricing &&
                        item.apimaster_price_ratio != null &&
                        item.apimaster_price_ratio !== 1 && (
                          <span className='ml-1 text-[10px] font-normal text-emerald-500'>
                            ×{item.apimaster_price_ratio.toFixed(2)}
                          </span>
                        )}
                    </td>
                    <td
                      className={`px-2 py-2.5 text-right text-gray-500 tabular-nums ${dim}`}
                    >
                      {fmtPrice(item.hub_price)}
                    </td>
                    <td
                      className={`px-3 py-2.5 text-right text-gray-600 tabular-nums ${dim}`}
                    >
                      {item.latency_median_ms > 0 ? (
                        `${(item.latency_median_ms / 1000).toFixed(1)} s`
                      ) : (
                        <span className='text-gray-300'>—</span>
                      )}
                    </td>
                    <td
                      className={`px-3 py-2.5 text-right text-gray-600 tabular-nums ${dim}`}
                    >
                      {item.latency_p95_ms > 0 ? (
                        `${(item.latency_p95_ms / 1000).toFixed(1)} s`
                      ) : (
                        <span className='text-gray-300'>—</span>
                      )}
                    </td>
                    <td
                      className={`px-3 py-2.5 text-right tabular-nums ${dim}`}
                    >
                      {item.latency_cv_pct > 0 ? (
                        <span
                          className={
                            item.latency_cv_pct > 60
                              ? 'text-rose-500'
                              : item.latency_cv_pct > 30
                                ? 'text-amber-500'
                                : 'text-gray-500'
                          }
                        >
                          {item.latency_cv_pct.toFixed(0)}%
                        </span>
                      ) : (
                        <span className='text-gray-300'>—</span>
                      )}
                    </td>
                    <td className={`px-3 py-2.5 ${dim}`}>
                      {isFreeModel ? (
                        <span className='text-gray-300'>—</span>
                      ) : (
                        <DotGrid
                          history={item.fingerprint_history}
                          onAnalyze={
                            item.base_url
                              ? () => handleAnalyze(item)
                              : undefined
                          }
                        />
                      )}
                    </td>
                    <td className={`px-3 py-2.5 ${dim}`}>
                      <DotGrid history={item.uptime_history} />
                    </td>
                    <td className='px-3 py-2.5 text-center'>
                      <div className='flex items-center justify-center gap-2'>
                        {!isFreeModel && (
                          <button
                            onClick={() => detectNow(item.channel_id)}
                            disabled={
                              !!detectingChannels[
                                `${item.channel_id}-${activeModel}`
                              ]
                            }
                            className='rounded-md border border-blue-200 px-2.5 py-1 text-xs whitespace-nowrap text-blue-600 transition-colors hover:bg-blue-50 hover:text-blue-700 disabled:cursor-not-allowed disabled:opacity-50'
                            title={t(
                              'Trigger a fingerprint detection now, result shows in the Detection Result column in ~15-20s'
                            )}
                          >
                            {detectingChannels[
                              `${item.channel_id}-${activeModel}`
                            ]
                              ? t('Detecting…')
                              : t('Manual Detect')}
                          </button>
                        )}
                        <button
                          onClick={() => pingNow(item.channel_id)}
                          disabled={
                            !!pingingChannels[
                              `${item.channel_id}-${activeModel}`
                            ]
                          }
                          className='rounded-md border border-emerald-200 px-2.5 py-1 text-xs whitespace-nowrap text-emerald-600 transition-colors hover:bg-emerald-50 hover:text-emerald-700 disabled:cursor-not-allowed disabled:opacity-50'
                          title={t(
                            'Trigger an uptime check now, result shows in the Uptime Status column in ~8s'
                          )}
                        >
                          {pingingChannels[`${item.channel_id}-${activeModel}`]
                            ? t('Pinging…')
                            : t('Manual Ping')}
                        </button>
                        {isFreeModel && item.free_model_config && (
                          <button
                            onClick={() =>
                              setFreeMemberEdit({
                                channelName: item.channel_name,
                                config: { ...item.free_model_config! },
                              })
                            }
                            className='rounded-md border border-violet-200 px-2.5 py-1 text-xs whitespace-nowrap text-violet-600 transition-colors hover:bg-violet-50 hover:text-violet-700'
                          >
                            {t('Routing Config')}
                          </button>
                        )}
                        {isFreeModel && (
                          <button
                            onClick={() => openNumericEdit('route-price', item)}
                            className='rounded-md border border-orange-200 px-2.5 py-1 text-xs whitespace-nowrap text-orange-600 transition-colors hover:bg-orange-50 hover:text-orange-700'
                          >
                            {t('Edit Route Price')}
                          </button>
                        )}
                        <button
                          onClick={() =>
                            toggleChannel(item.channel_id, isEffectivelyEnabled)
                          }
                          className={
                            isEffectivelyEnabled
                              ? 'rounded-md border border-red-200 px-2.5 py-1 text-xs text-red-600 transition-colors hover:bg-red-50 hover:text-red-700'
                              : 'rounded-md border border-emerald-200 px-2.5 py-1 text-xs text-emerald-600 transition-colors hover:bg-emerald-50 hover:text-emerald-700'
                          }
                        >
                          {isEffectivelyEnabled ? t('Disable') : t('Enable')}
                        </button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>

        <IntervalDialog
          open={intervalOpen === 'fingerprint'}
          onClose={() => setIntervalOpen(null)}
          initialMinutes={config.fingerprint_interval_minutes}
          onSave={(m) => saveConfig({ fingerprint_interval_minutes: m })}
        />
        <IntervalDialog
          open={intervalOpen === 'uptime'}
          onClose={() => setIntervalOpen(null)}
          initialMinutes={config.uptime_interval_minutes}
          onSave={(m) => saveConfig({ uptime_interval_minutes: m })}
        />
        {analysis && (
          <AnalysisModal state={analysis} onClose={() => setAnalysis(null)} />
        )}
        <Dialog
          open={numericEdit !== null}
          onOpenChange={(open) => !open && setNumericEdit(null)}
        >
          <DialogContent className='max-w-sm'>
            <DialogHeader>
              <DialogTitle>{t('Edit Route Price')}</DialogTitle>
            </DialogHeader>
            <div className='space-y-3 py-2'>
              <p className='text-sm text-gray-500'>
                {numericEdit?.channelName}
              </p>
              <label className='space-y-1 text-sm'>
                <span>{t('Route Price')}</span>
                <Input
                  type='number'
                  min='0.00000001'
                  step='0.0001'
                  value={numericEdit?.value ?? ''}
                  autoFocus
                  onChange={(event) =>
                    setNumericEdit((current) =>
                      current ? { ...current, value: event.target.value } : null
                    )
                  }
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') void saveNumericEdit()
                  }}
                />
              </label>
              {numericEdit?.kind === 'route-price' && (
                <p className='text-xs text-gray-500'>
                  {t(
                    'Retained for compatibility only; capability, health, priority, and weight now control routing. Users are not charged.'
                  )}
                </p>
              )}
            </div>
            <DialogFooter>
              <Button
                variant='outline'
                onClick={() => setNumericEdit(null)}
                disabled={numericEditSaving}
              >
                {t('Cancel')}
              </Button>
              <Button
                onClick={() => void saveNumericEdit()}
                disabled={numericEditSaving || !numericEdit?.value}
              >
                {numericEditSaving ? t('Saving...') : t('Save')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
        <Dialog open={freeSettingsOpen} onOpenChange={setFreeSettingsOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{t('FreeModel Settings')}</DialogTitle>
            </DialogHeader>
            <div className='space-y-4 py-2'>
              <label className='flex items-center justify-between gap-4 text-sm'>
                <span>{t('Enable cumulative paid eligibility')}</span>
                <Switch
                  checked={freeSettings.cumulative_paid_enabled}
                  onCheckedChange={(value) =>
                    setFreeSettings((current) => ({
                      ...current,
                      cumulative_paid_enabled: value,
                    }))
                  }
                />
              </label>
              <label className='space-y-1 text-sm'>
                <span>{t('Minimum cumulative paid USD')}</span>
                <Input
                  type='number'
                  min='0'
                  step='0.01'
                  value={freeSettings.minimum_cumulative_paid_usd}
                  onChange={(event) =>
                    setFreeSettings((current) => ({
                      ...current,
                      minimum_cumulative_paid_usd: Number(event.target.value),
                    }))
                  }
                />
              </label>
              <label className='flex items-center justify-between gap-4 text-sm'>
                <span>{t('Enable active subscription eligibility')}</span>
                <Switch
                  checked={freeSettings.active_subscription_enabled}
                  onCheckedChange={(value) =>
                    setFreeSettings((current) => ({
                      ...current,
                      active_subscription_enabled: value,
                    }))
                  }
                />
              </label>
              <label className='space-y-1 text-sm'>
                <span>{t('Minimum active subscription price USD')}</span>
                <Input
                  type='number'
                  min='0'
                  step='0.01'
                  value={freeSettings.minimum_subscription_price_usd}
                  onChange={(event) =>
                    setFreeSettings((current) => ({
                      ...current,
                      minimum_subscription_price_usd: Number(
                        event.target.value
                      ),
                    }))
                  }
                />
              </label>
              <label className='space-y-1 text-sm'>
                <span>{t('Requests per account per minute')}</span>
                <Input
                  type='number'
                  min='1'
                  step='1'
                  value={freeSettings.account_requests_per_minute}
                  onChange={(event) =>
                    setFreeSettings((current) => ({
                      ...current,
                      account_requests_per_minute: Number(event.target.value),
                    }))
                  }
                />
              </label>
              <label className='space-y-1 text-sm'>
                <span>{t('Maximum attempts per request')}</span>
                <Input
                  type='number'
                  min='1'
                  max='20'
                  step='1'
                  value={freeSettings.max_attempts}
                  onChange={(event) =>
                    setFreeSettings((current) => ({
                      ...current,
                      max_attempts: Number(event.target.value),
                    }))
                  }
                />
              </label>
              <div className='rounded-md border border-gray-200 bg-gray-50 p-3 text-xs text-gray-600'>
                {t(
                  'Routing policy: higher priority first; channels at the same priority are selected by weighted random without replacement. Route Price is retained for compatibility only. Paid fallback is disabled.'
                )}
              </div>
            </div>
            <DialogFooter>
              <Button
                variant='outline'
                onClick={() => setFreeSettingsOpen(false)}
              >
                {t('Cancel')}
              </Button>
              <Button onClick={saveFreeSettings}>{t('Save')}</Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
        <Dialog
          open={!!freeMemberEdit}
          onOpenChange={(open) => !open && setFreeMemberEdit(null)}
        >
          <DialogContent className='max-w-xl'>
            <DialogHeader>
              <DialogTitle>
                {t('FreeModel Routing Config')} · {freeMemberEdit?.channelName}
              </DialogTitle>
            </DialogHeader>
            {freeMemberEdit && (
              <div className='space-y-4 py-2'>
                <label className='flex items-center justify-between text-sm'>
                  <span>{t('Member enabled')}</span>
                  <Switch
                    checked={freeMemberEdit.config.enabled}
                    onCheckedChange={(value) =>
                      setFreeMemberEdit((current) =>
                        current
                          ? {
                              ...current,
                              config: { ...current.config, enabled: value },
                            }
                          : null
                      )
                    }
                  />
                </label>
                <div className='grid grid-cols-2 gap-3'>
                  <label className='space-y-1 text-sm'>
                    <span>{t('Priority (higher first)')}</span>
                    <Input
                      type='number'
                      value={freeMemberEdit.config.priority}
                      onChange={(event) =>
                        setFreeMemberEdit((current) =>
                          current
                            ? {
                                ...current,
                                config: {
                                  ...current.config,
                                  priority: Number(event.target.value),
                                },
                              }
                            : null
                        )
                      }
                    />
                  </label>
                  <label className='space-y-1 text-sm'>
                    <span>{t('Weight')}</span>
                    <Input
                      type='number'
                      min='1'
                      value={freeMemberEdit.config.weight}
                      onChange={(event) =>
                        setFreeMemberEdit((current) =>
                          current
                            ? {
                                ...current,
                                config: {
                                  ...current.config,
                                  weight: Number(event.target.value),
                                },
                              }
                            : null
                        )
                      }
                    />
                  </label>
                  <label className='space-y-1 text-sm'>
                    <span>{t('Codex priority (empty = inherit)')}</span>
                    <Input
                      type='number'
                      value={freeMemberEdit.config.codex_priority ?? ''}
                      placeholder={String(freeMemberEdit.config.priority)}
                      onChange={(event) =>
                        setFreeMemberEdit((current) =>
                          current
                            ? {
                                ...current,
                                config: {
                                  ...current.config,
                                  codex_priority:
                                    event.target.value === ''
                                      ? null
                                      : Number(event.target.value),
                                },
                              }
                            : null
                        )
                      }
                    />
                  </label>
                  <label className='space-y-1 text-sm'>
                    <span>{t('Codex weight (empty = inherit)')}</span>
                    <Input
                      type='number'
                      min='1'
                      value={freeMemberEdit.config.codex_weight ?? ''}
                      placeholder={String(freeMemberEdit.config.weight)}
                      onChange={(event) =>
                        setFreeMemberEdit((current) =>
                          current
                            ? {
                                ...current,
                                config: {
                                  ...current.config,
                                  codex_weight:
                                    event.target.value === ''
                                      ? null
                                      : Number(event.target.value),
                                },
                              }
                            : null
                        )
                      }
                    />
                  </label>
                  <label className='space-y-1 text-sm'>
                    <span>{t('Maximum context tokens')}</span>
                    <Input
                      type='number'
                      min='1'
                      value={freeMemberEdit.config.max_context_tokens}
                      onChange={(event) =>
                        setFreeMemberEdit((current) =>
                          current
                            ? {
                                ...current,
                                config: {
                                  ...current.config,
                                  max_context_tokens: Number(
                                    event.target.value
                                  ),
                                },
                              }
                            : null
                        )
                      }
                    />
                  </label>
                  <label className='space-y-1 text-sm'>
                    <span>{t('Timeout (ms)')}</span>
                    <Input
                      type='number'
                      min='100'
                      value={freeMemberEdit.config.timeout_ms}
                      onChange={(event) =>
                        setFreeMemberEdit((current) =>
                          current
                            ? {
                                ...current,
                                config: {
                                  ...current.config,
                                  timeout_ms: Number(event.target.value),
                                },
                              }
                            : null
                        )
                      }
                    />
                  </label>
                  <label className='space-y-1 text-sm'>
                    <span>{t('Daily request limit (0 = unlimited)')}</span>
                    <Input
                      type='number'
                      min='0'
                      value={freeMemberEdit.config.daily_request_limit}
                      onChange={(event) =>
                        setFreeMemberEdit((current) =>
                          current
                            ? {
                                ...current,
                                config: {
                                  ...current.config,
                                  daily_request_limit: Number(
                                    event.target.value
                                  ),
                                },
                              }
                            : null
                        )
                      }
                    />
                  </label>
                  <label className='space-y-1 text-sm'>
                    <span>{t('Shared daily limit group')}</span>
                    <Input
                      value={
                        freeMemberEdit.config.daily_request_limit_group || ''
                      }
                      placeholder={t('Blank means per-channel limit')}
                      onChange={(event) =>
                        setFreeMemberEdit((current) =>
                          current
                            ? {
                                ...current,
                                config: {
                                  ...current.config,
                                  daily_request_limit_group: event.target.value,
                                },
                              }
                            : null
                        )
                      }
                    />
                  </label>
                </div>
                <div className='grid grid-cols-2 gap-2 rounded-md border border-gray-200 p-3'>
                  {(
                    [
                      'text',
                      'vision',
                      'tools',
                      'required_tool_call',
                      'json_object',
                      'json_schema',
                    ] as const
                  ).map((capability) => (
                    <label
                      key={capability}
                      className='flex items-center justify-between gap-3 text-sm'
                    >
                      <span>{capability}</span>
                      <Switch
                        checked={freeMemberEdit.config.capabilities[capability]}
                        onCheckedChange={(value) =>
                          setFreeMemberEdit((current) =>
                            current
                              ? {
                                  ...current,
                                  config: {
                                    ...current.config,
                                    capabilities: {
                                      ...current.config.capabilities,
                                      [capability]: value,
                                    },
                                  },
                                }
                              : null
                          )
                        }
                      />
                    </label>
                  ))}
                  <label className='flex items-center justify-between gap-3 text-sm'>
                    <span>{t('Codex tools')}</span>
                    <Switch
                      checked={
                        freeMemberEdit.config.capabilities.codex_tools ??
                        freeMemberEdit.config.capabilities.tools
                      }
                      onCheckedChange={(value) =>
                        setFreeMemberEdit((current) =>
                          current
                            ? {
                                ...current,
                                config: {
                                  ...current.config,
                                  capabilities: {
                                    ...current.config.capabilities,
                                    codex_tools: value,
                                  },
                                },
                              }
                            : null
                        )
                      }
                    />
                  </label>
                </div>
                <div className='space-y-2 rounded-md border border-gray-200 p-3'>
                  <div className='text-xs font-semibold text-gray-500'>
                    {t('Endpoints')}
                  </div>
                  {(
                    [
                      ['chat_completions', 'Chat Completions'],
                      ['responses', 'Responses API'],
                      ['messages', 'Messages API'],
                    ] as const
                  ).map(([endpoint, label]) => (
                    <label
                      key={endpoint}
                      className='flex items-center justify-between gap-3 text-sm'
                    >
                      <span>{label}</span>
                      <Switch
                        checked={freeMemberEdit.config.endpoints[endpoint]}
                        onCheckedChange={(value) =>
                          setFreeMemberEdit((current) =>
                            current
                              ? {
                                  ...current,
                                  config: {
                                    ...current.config,
                                    endpoints: {
                                      ...current.config.endpoints,
                                      [endpoint]: value,
                                    },
                                  },
                                }
                              : null
                          )
                        }
                      />
                    </label>
                  ))}
                </div>
                {(() => {
                  const health = data.find(
                    (item) =>
                      item.channel_id === freeMemberEdit.config.channel_id
                  )?.free_model_health
                  if (!health) return null
                  return (
                    <div className='rounded-md bg-gray-50 p-3 text-xs text-gray-600'>
                      {t('Health')}: {health.status} · {t('Success rate')}:{' '}
                      {(health.recent_success_rate * 100).toFixed(1)}% ·{' '}
                      {t('Latency')}: {health.latency_ms.toFixed(0)} ms ·{' '}
                      {t('Cooldown')}:{' '}
                      {Math.ceil(
                        Math.max(
                          health.cooldown_remaining_ms,
                          health.circuit_remaining_ms,
                          health.quarantine_remaining_ms
                        ) / 1000
                      )}{' '}
                      s
                      {health.last_failure_reason
                        ? ` · ${health.last_failure_reason}`
                        : ''}
                    </div>
                  )
                })()}
              </div>
            )}
            <DialogFooter>
              <Button variant='outline' onClick={() => setFreeMemberEdit(null)}>
                {t('Cancel')}
              </Button>
              <Button
                onClick={() => void saveFreeMember()}
                disabled={freeMemberSaving}
              >
                {freeMemberSaving ? t('Saving...') : t('Save')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
