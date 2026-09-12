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
import { useCallback, useEffect, useState } from 'react'
import {
  getCoreRowModel,
  useReactTable,
  type PaginationState,
} from '@tanstack/react-table'
import {
  Eraser,
  Languages,
  Loader2,
  Pencil,
  Plus,
  Save,
  Trash2,
  X,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTablePagination } from '@/components/data-table/pagination'
import { SectionPageLayout } from '@/components/layout'
import { StatusBadge } from '@/components/status-badge'

/**
 * Announcements shown in the APIMaster site header and popup.
 *
 * The records live in the APIMaster Postgres database, not in new-api options,
 * because the site must render them for signed-out visitors too. The bundled
 * SPA is proxied same-origin under /_panel/, so this relative path reaches the
 * APIMaster admin routes directly (they authenticate with the APIMaster
 * session cookie, which is sent for same-origin requests).
 */
const ADMIN_API = '/api/admin/announcements'

/** Locales the official site ships, in the order the editor tab bar shows them. */
const LOCALES = [
  'zh',
  'zh-TW',
  'en',
  'ja',
  'pt',
  'de',
  'fr',
  'tr',
  'it',
  'pl',
  'id',
  'ko',
  'es',
  'ru',
  'vi',
] as const

type LocaleCode = (typeof LOCALES)[number]

type Level = 'info' | 'warning' | 'critical'

type AdminAnnouncement = {
  id: string
  slug: string
  level: Level
  title: Record<string, string>
  body: Record<string, string>
  publishedAt: string
  expiresAt: string | null
  active: boolean
  createdBy: string | null
  createdAt: string
  updatedAt: string
}

type Draft = {
  id: string | null
  slug: string
  level: Level
  title: Record<string, string>
  body: Record<string, string>
  publishedAt: string
  expiresAt: string
  active: boolean
}

const LEVELS: {
  value: Level
  labelKey: string
  variant: 'info' | 'warning' | 'danger'
}[] = [
  { value: 'info', labelKey: 'Info', variant: 'info' },
  { value: 'warning', labelKey: 'Warning', variant: 'warning' },
  { value: 'critical', labelKey: 'Critical', variant: 'danger' },
]

function levelVariant(level: Level) {
  return LEVELS.find((item) => item.value === level)?.variant ?? 'info'
}

/**
 * Where an announcement currently stands, in the order the checks matter:
 * never published, scheduled for later, taken down, or live on the site.
 *
 * "Taken down" is an expiry in the past, not `active = false` — taking a
 * notice down sets its expiry to now so the record survives as history.
 */
type AnnouncementStatus = 'draft' | 'scheduled' | 'unlisted' | 'published'

const STATUS_STYLES: Record<
  AnnouncementStatus,
  {
    labelKey: string
    variant: 'success' | 'warning' | 'neutral'
    hintKey: string
  }
> = {
  draft: {
    labelKey: 'Unpublished',
    variant: 'warning',
    hintKey: 'Drafts are not published yet, so there is nothing to take down.',
  },
  scheduled: {
    labelKey: 'Scheduled',
    variant: 'warning',
    hintKey: 'Not published yet — the publish time is still in the future.',
  },
  unlisted: {
    labelKey: 'Unlisted',
    variant: 'neutral',
    hintKey: 'Already taken down. Edit the expiry time to bring it back.',
  },
  published: {
    labelKey: 'Published',
    variant: 'success',
    hintKey: 'Live on the website — take it down to stop the popup.',
  },
}

function announcementStatus(
  item: AdminAnnouncement,
  now = Date.now()
): AnnouncementStatus {
  if (!item.active) return 'draft'
  const published = Date.parse(item.publishedAt)
  if (!Number.isNaN(published) && published > now) return 'scheduled'
  if (item.expiresAt) {
    const expires = Date.parse(item.expiresAt)
    // An unparseable expiry cannot be trusted; treat it as still live.
    if (!Number.isNaN(expires) && expires <= now) return 'unlisted'
  }
  return 'published'
}

/** `<input type="datetime-local">` wants `YYYY-MM-DDTHH:mm` in local time. */
function toLocalInput(iso: string | null): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(
    date.getHours()
  )}:${pad(date.getMinutes())}`
}

function fromLocalInput(value: string): string | null {
  if (!value) return null
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? null : date.toISOString()
}

function formatMoment(iso: string | null): string {
  if (!iso) return '—'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '—'
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}/${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${pad(
    date.getHours()
  )}:${pad(date.getMinutes())}`
}

const hasText = (value: string | undefined) => Boolean(value && value.trim())

/** A locale counts as filled if either field has content. */
function isFilled(draft: Draft, code: string): boolean {
  return hasText(draft.title[code]) || hasText(draft.body[code])
}

/**
 * Which locale to translate *from*. Chinese is the language the team writes in,
 * so it wins whenever it has content; otherwise fall back to the first filled
 * locale so an English-only draft still works.
 */
function pickSourceLocale(draft: Draft): LocaleCode | null {
  const ordered = [
    'zh',
    ...LOCALES.filter((code) => code !== 'zh'),
  ] as LocaleCode[]
  return ordered.find((code) => isFilled(draft, code)) ?? null
}

/**
 * Run `worker` over `items` with a bounded number in flight. Translation is one
 * request per locale, so this keeps a full batch to a few seconds without
 * hammering the gateway with 14 at once.
 */
async function mapWithConcurrency<T>(
  items: T[],
  limit: number,
  worker: (item: T) => Promise<void>
): Promise<void> {
  let cursor = 0
  const runners = Array.from(
    { length: Math.min(limit, items.length) },
    async () => {
      while (cursor < items.length) {
        const item = items[cursor]
        cursor += 1
        await worker(item)
      }
    }
  )
  await Promise.all(runners)
}

/** How many locales are translated at once. */
const TRANSLATE_CONCURRENCY = 3

function emptyDraft(): Draft {
  return {
    id: null,
    slug: '',
    level: 'info',
    title: { zh: '', en: '' },
    body: { zh: '', en: '' },
    publishedAt: toLocalInput(new Date().toISOString()),
    expiresAt: '',
    active: false,
  }
}

export function SiteAnnouncementsPage() {
  const { t } = useTranslation()

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Site Announcements')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <AnnouncementManager />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

/**
 * Announcement CRUD against the APIMaster admin API.
 */
function AnnouncementManager() {
  const { t } = useTranslation()
  const [items, setItems] = useState<AdminAnnouncement[]>([])
  const [total, setTotal] = useState(0)
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 20,
  })
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [editLocale, setEditLocale] = useState<LocaleCode>('zh')
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AdminAnnouncement | null>(
    null
  )
  const [unpublishTarget, setUnpublishTarget] =
    useState<AdminAnnouncement | null>(null)
  const [translating, setTranslating] = useState(false)
  const [translateProgress, setTranslateProgress] = useState<{
    done: number
    total: number
  } | null>(null)
  const [translateError, setTranslateError] = useState<string | null>(null)
  const [overwrite, setOverwrite] = useState(false)
  const [clearTarget, setClearTarget] = useState<{
    dropped: LocaleCode[]
    source: LocaleCode
  } | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      // Server-side paging: the table is the only consumer, and the list is
      // unbounded, so never pull every row just to slice it in the browser.
      const query = new URLSearchParams({
        page: String(pagination.pageIndex + 1),
        pageSize: String(pagination.pageSize),
      })
      const res = await fetch(`${ADMIN_API}?${query}`, {
        credentials: 'include',
      })
      if (!res.ok) {
        setItems([])
        setTotal(0)
        setLoadError(
          res.status === 403
            ? t('Only APIMaster administrators can manage site announcements')
            : t('Failed to load announcements')
        )
        return
      }
      const data = (await res.json()) as {
        items?: AdminAnnouncement[]
        total?: number
      }
      setItems(Array.isArray(data.items) ? data.items : [])
      setTotal(typeof data.total === 'number' ? data.total : 0)
      setLoadError(null)
    } catch {
      setItems([])
      setTotal(0)
      setLoadError(t('Failed to load announcements'))
    } finally {
      setLoading(false)
    }
  }, [t, pagination])

  useEffect(() => {
    void load()
  }, [load])

  // Deleting the last row of the last page would otherwise leave the user on a
  // blank page; step back one page and let the effect above refetch.
  useEffect(() => {
    const lastPage = Math.max(0, Math.ceil(total / pagination.pageSize) - 1)
    if (pagination.pageIndex > lastPage) {
      setPagination((prev) => ({ ...prev, pageIndex: lastPage }))
    }
  }, [total, pagination.pageSize, pagination.pageIndex])

  /**
   * Only for the pager state — the table body is rendered by hand below, so no
   * column definitions are needed.
   */
  const table = useReactTable({
    data: items,
    columns: [],
    state: { pagination },
    onPaginationChange: setPagination,
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    pageCount: Math.ceil(total / pagination.pageSize),
    rowCount: total,
  })

  function openCreate() {
    setFormError(null)
    setTranslateError(null)
    setEditLocale('zh')
    setDraft(emptyDraft())
  }

  function openEdit(item: AdminAnnouncement) {
    setFormError(null)
    setTranslateError(null)
    setEditLocale('zh')
    setDraft({
      id: item.id,
      slug: item.slug,
      level: item.level,
      title: { ...item.title },
      body: { ...item.body },
      publishedAt: toLocalInput(item.publishedAt),
      expiresAt: toLocalInput(item.expiresAt),
      active: item.active,
    })
  }

  function setLocalized(field: 'title' | 'body', value: string) {
    setDraft((prev) =>
      prev
        ? { ...prev, [field]: { ...prev[field], [editLocale]: value } }
        : prev
    )
  }

  /**
   * Translate the source locale into every other locale, one request per
   * locale so a single failure only costs that one language. Results land in
   * the draft, which still has to be saved explicitly.
   */
  async function translateAll() {
    if (!draft || translating) return

    const source = pickSourceLocale(draft)
    if (!source) {
      setTranslateError(
        t('Write a title or body in at least one language first')
      )
      return
    }

    const targets = LOCALES.filter(
      (code) => code !== source && (overwrite || !isFilled(draft, code))
    )
    if (targets.length === 0) {
      setTranslateError(
        t(
          'Every language is already translated. Tick "Overwrite existing translations" to redo them.'
        )
      )
      return
    }

    const sourceTitle = draft.title[source] ?? ''
    const sourceBody = draft.body[source] ?? ''

    setTranslating(true)
    setTranslateError(null)
    setTranslateProgress({ done: 0, total: targets.length })

    const failed: string[] = []
    let done = 0
    const total = targets.length

    await mapWithConcurrency(targets, TRANSLATE_CONCURRENCY, async (code) => {
      try {
        const res = await fetch(`${ADMIN_API}/translate`, {
          method: 'POST',
          credentials: 'include',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            target: code,
            title: sourceTitle,
            body: sourceBody,
          }),
        })
        const data = (await res.json().catch(() => ({}))) as {
          title?: string
          body?: string
        }
        if (!res.ok) {
          failed.push(code)
          return
        }
        // Functional update: the surrounding `draft` closure is stale by now.
        setDraft((prev) =>
          prev
            ? {
                ...prev,
                title: {
                  ...prev.title,
                  [code]: data.title ?? prev.title[code] ?? '',
                },
                body: {
                  ...prev.body,
                  [code]: data.body ?? prev.body[code] ?? '',
                },
              }
            : prev
        )
      } catch {
        failed.push(code)
      } finally {
        done += 1
        setTranslateProgress({ done, total })
      }
    })

    setTranslating(false)
    setTranslateProgress(null)
    setTranslateError(
      failed.length > 0
        ? t('These languages failed, you can retry: {{locales}}', {
            locales: failed.join(', '),
          })
        : null
    )
  }

  /** Drop every translation, keeping the source locale, so be exact about it. */
  function clearTranslations() {
    if (!draft || translating) return

    const source = pickSourceLocale(draft) ?? 'zh'
    const dropped = LOCALES.filter(
      (code) => code !== source && isFilled(draft, code)
    )
    if (dropped.length === 0) {
      setTranslateError(t('There is nothing to clear'))
      return
    }

    // The console runs inside a sandboxed iframe without `allow-modals`, so
    // window.confirm() is silently swallowed and returns false — a native
    // confirm here reads as "the button does nothing". Ask in-page instead.
    setClearTarget({ dropped, source })
  }

  function applyClearTranslations() {
    if (!draft || !clearTarget) return

    const { dropped, source } = clearTarget
    setClearTarget(null)

    const title: Record<string, string> = {}
    const body: Record<string, string> = {}
    if (hasText(draft.title[source])) title[source] = draft.title[source]
    if (hasText(draft.body[source])) body[source] = draft.body[source]

    setTranslateError(null)
    setDraft({ ...draft, title, body })
    setEditLocale(source)
    toast.success(
      t('Cleared {{count}} languages, save to apply.', {
        count: dropped.length,
      })
    )
  }

  async function save() {
    if (!draft) return
    if (!draft.slug.trim()) {
      setFormError(t('Slug is required'))
      return
    }
    if (!Object.values(draft.title).some((value) => value.trim())) {
      setFormError(t('Fill in at least one language title'))
      return
    }
    const publishedAt = fromLocalInput(draft.publishedAt)
    if (!publishedAt) {
      setFormError(t('Publish time is required'))
      return
    }

    setSaving(true)
    setFormError(null)
    try {
      const res = await fetch(
        draft.id ? `${ADMIN_API}/${draft.id}` : ADMIN_API,
        {
          method: draft.id ? 'PATCH' : 'POST',
          credentials: 'include',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            slug: draft.slug.trim(),
            level: draft.level,
            title: draft.title,
            body: draft.body,
            publishedAt,
            expiresAt: fromLocalInput(draft.expiresAt),
            active: draft.active,
          }),
        }
      )
      if (!res.ok) {
        const data = (await res.json().catch(() => ({}))) as { error?: string }
        setFormError(data.error ?? t('Failed to save announcement'))
        return
      }
      toast.success(t('Announcement saved'))
      setDraft(null)
      await load()
    } catch {
      setFormError(t('Failed to save announcement'))
    } finally {
      setSaving(false)
    }
  }

  /**
   * Taking a notice down sets its expiry to now. The row stays published, so
   * it keeps its place in the website's announcement history — visitors just
   * stop getting the popup.
   */
  async function confirmUnpublish() {
    const target = unpublishTarget
    setUnpublishTarget(null)
    if (!target) return
    const res = await fetch(`${ADMIN_API}/${target.id}`, {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ expiresAt: new Date().toISOString() }),
    })
    if (!res.ok) {
      toast.error(t('Failed to save announcement'))
      return
    }
    toast.success(t('Announcement unpublished'))
    await load()
  }

  async function confirmDelete() {
    const target = deleteTarget
    setDeleteTarget(null)
    if (!target) return
    const res = await fetch(`${ADMIN_API}/${target.id}`, {
      method: 'DELETE',
      credentials: 'include',
    })
    if (!res.ok) {
      toast.error(t('Failed to delete announcement'))
      return
    }
    toast.success(t('Announcement deleted'))
    await load()
  }

  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Shown on the APIMaster website header and as an unread popup. Signed-out visitors see them too. Markdown is supported; line breaks are preserved.'
        )}
      </p>

      <div className='flex flex-wrap items-center gap-2'>
        <Button type='button' onClick={openCreate}>
          <Plus className='size-4' />
          {t('New Announcement')}
        </Button>
        <Button type='button' variant='outline' onClick={() => void load()}>
          {t('Refresh')}
        </Button>
      </div>

      {loadError ? (
        <p className='text-destructive text-sm'>{loadError}</p>
      ) : loading ? (
        <div className='text-muted-foreground flex items-center gap-2 py-8 text-sm'>
          <Loader2 className='size-4 animate-spin' />
          {t('Loading')}
        </div>
      ) : items.length === 0 ? (
        <p className='text-muted-foreground py-8 text-sm'>
          {t(
            'No announcements yet. Create one to publish a notice on the website.'
          )}
        </p>
      ) : (
        <div className='space-y-3'>
          <div className='rounded-lg border'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Title')}</TableHead>
                  <TableHead>{t('Slug')}</TableHead>
                  <TableHead>{t('Level')}</TableHead>
                  <TableHead>{t('Publish Date')}</TableHead>
                  <TableHead>{t('Expires At')}</TableHead>
                  <TableHead>{t('Status')}</TableHead>
                  <TableHead className='text-right'>{t('Actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((item) => {
                  const status = announcementStatus(item)
                  const statusStyle = STATUS_STYLES[status]
                  return (
                    <TableRow key={item.id}>
                      <TableCell className='max-w-[240px] truncate font-medium'>
                        {item.title.zh || item.title.en || item.slug}
                      </TableCell>
                      <TableCell className='text-muted-foreground font-mono text-xs'>
                        {item.slug}
                      </TableCell>
                      <TableCell>
                        <StatusBadge
                          variant={levelVariant(item.level)}
                          label={t(
                            LEVELS.find((level) => level.value === item.level)
                              ?.labelKey ?? 'Info'
                          )}
                        />
                      </TableCell>
                      <TableCell className='text-xs whitespace-nowrap'>
                        {formatMoment(item.publishedAt)}
                      </TableCell>
                      <TableCell className='text-xs whitespace-nowrap'>
                        {item.expiresAt
                          ? formatMoment(item.expiresAt)
                          : t('Never')}
                      </TableCell>
                      <TableCell>
                        <StatusBadge
                          variant={statusStyle.variant}
                          label={t(statusStyle.labelKey)}
                        />
                      </TableCell>
                      <TableCell>
                        <div className='flex items-center justify-end gap-1'>
                          <Button
                            type='button'
                            size='sm'
                            variant='outline'
                            disabled={status !== 'published'}
                            title={t(statusStyle.hintKey)}
                            onClick={() => setUnpublishTarget(item)}
                          >
                            {t('Unpublish')}
                          </Button>
                          <Button
                            type='button'
                            size='sm'
                            variant='ghost'
                            onClick={() => openEdit(item)}
                            title={t('Edit')}
                          >
                            <Pencil className='size-4' />
                          </Button>
                          <Button
                            type='button'
                            size='sm'
                            variant='ghost'
                            onClick={() => setDeleteTarget(item)}
                            title={t('Delete')}
                          >
                            <Trash2 className='text-destructive size-4' />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
          <DataTablePagination table={table} />
        </div>
      )}

      {draft ? (
        <Dialog open onOpenChange={(open) => (!open ? setDraft(null) : null)}>
          <DialogContent className='max-h-[88vh] grid-rows-[auto_minmax(0,1fr)_auto] sm:max-w-4xl lg:max-w-5xl'>
            <DialogHeader>
              <DialogTitle>
                {draft.id ? t('Edit Announcement') : t('New Announcement')}
              </DialogTitle>
              <DialogDescription>
                {t(
                  'Takes effect immediately after saving. Missing languages fall back to English.'
                )}
              </DialogDescription>
            </DialogHeader>

            <div className='min-h-0 space-y-4 overflow-y-auto pr-1'>
              <div className='grid gap-4 sm:grid-cols-2'>
                <div className='space-y-2'>
                  <label className='text-sm font-medium'>{t('Slug')}</label>
                  <Input
                    value={draft.slug}
                    placeholder='gpt-instability-2026-09'
                    onChange={(event) =>
                      setDraft({ ...draft, slug: event.target.value })
                    }
                  />
                </div>
                <div className='space-y-2'>
                  <label className='text-sm font-medium'>{t('Level')}</label>
                  <Select
                    value={draft.level}
                    onValueChange={(value) =>
                      setDraft({ ...draft, level: value as Level })
                    }
                  >
                    <SelectTrigger className='w-full'>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        {LEVELS.map((level) => (
                          <SelectItem key={level.value} value={level.value}>
                            {t(level.labelKey)}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </div>
                <div className='space-y-2'>
                  <label className='text-sm font-medium'>
                    {t('Publish Date')}
                  </label>
                  <Input
                    type='datetime-local'
                    value={draft.publishedAt}
                    onChange={(event) =>
                      setDraft({ ...draft, publishedAt: event.target.value })
                    }
                  />
                </div>
                <div className='space-y-2'>
                  <label className='text-sm font-medium'>
                    {t('Expires At')}
                    <span className='text-muted-foreground ml-1 text-xs font-normal'>
                      {t('(optional)')}
                    </span>
                  </label>
                  <div className='flex items-center gap-2'>
                    <Input
                      type='datetime-local'
                      value={draft.expiresAt}
                      onChange={(event) =>
                        setDraft({ ...draft, expiresAt: event.target.value })
                      }
                    />
                    {draft.expiresAt ? (
                      <Button
                        type='button'
                        size='sm'
                        variant='ghost'
                        onClick={() => setDraft({ ...draft, expiresAt: '' })}
                      >
                        <X className='size-4' />
                      </Button>
                    ) : null}
                  </div>
                </div>
              </div>

              <div className='flex items-center gap-3'>
                <Switch
                  checked={draft.active}
                  onCheckedChange={(checked: boolean) =>
                    setDraft({ ...draft, active: checked })
                  }
                />
                <div className='text-sm'>
                  <p className='font-medium'>{t('Publish now')}</p>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Off keeps it as a draft. Visitors never see drafts, even after the publish time.'
                    )}
                  </p>
                </div>
              </div>

              <div className='space-y-3'>
                <div className='flex flex-wrap items-center gap-2 border-b pb-3'>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    disabled={translating}
                    onClick={() => void translateAll()}
                  >
                    {translating ? (
                      <Loader2 className='size-4 animate-spin' />
                    ) : (
                      <Languages className='size-4' />
                    )}
                    {translating && translateProgress
                      ? t('Translating {{done}}/{{total}}', {
                          done: translateProgress.done,
                          total: translateProgress.total,
                        })
                      : t('Translate All')}
                  </Button>
                  <label className='text-muted-foreground flex items-center gap-1.5 text-xs'>
                    <input
                      type='checkbox'
                      className='size-3.5'
                      checked={overwrite}
                      onChange={(event) => setOverwrite(event.target.checked)}
                    />
                    {t('Overwrite existing translations')}
                  </label>
                  <Button
                    type='button'
                    size='sm'
                    variant='ghost'
                    className='ml-auto'
                    disabled={translating}
                    onClick={clearTranslations}
                  >
                    <Eraser className='size-4' />
                    {t('Clear translations')}
                  </Button>
                </div>
                {translateError ? (
                  <p className='text-xs text-amber-500'>{translateError}</p>
                ) : null}
                <div className='flex flex-wrap gap-1.5'>
                  {LOCALES.map((locale) => {
                    const filled = Boolean(
                      draft.title[locale]?.trim() || draft.body[locale]?.trim()
                    )
                    return (
                      <button
                        key={locale}
                        type='button'
                        onClick={() => setEditLocale(locale)}
                        className={`rounded-md border px-2.5 py-1 text-xs transition-colors ${
                          editLocale === locale
                            ? 'border-primary bg-primary text-primary-foreground'
                            : 'hover:bg-accent'
                        }`}
                      >
                        {locale}
                        {filled ? ' •' : ''}
                      </button>
                    )
                  })}
                </div>
                <Input
                  value={draft.title[editLocale] ?? ''}
                  placeholder={t('Title ({{locale}})', { locale: editLocale })}
                  onChange={(event) =>
                    setLocalized('title', event.target.value)
                  }
                />
                <Textarea
                  rows={12}
                  className='font-mono text-sm'
                  value={draft.body[editLocale] ?? ''}
                  placeholder={t('Body ({{locale}}), Markdown', {
                    locale: editLocale,
                  })}
                  onChange={(event) => setLocalized('body', event.target.value)}
                />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Line breaks are kept as-is; a blank line starts a new paragraph. "- " or "1. " starts a list.'
                  )}
                </p>
              </div>

              {formError ? (
                <p className='text-destructive text-sm'>{formError}</p>
              ) : null}
            </div>

            <DialogFooter>
              <Button
                type='button'
                variant='outline'
                onClick={() => setDraft(null)}
              >
                {t('Cancel')}
              </Button>
              <Button
                type='button'
                disabled={saving}
                onClick={() => void save()}
              >
                {saving ? (
                  <Loader2 className='size-4 animate-spin' />
                ) : (
                  <Save className='size-4' />
                )}
                {t('Save')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}

      <ConfirmDialog
        open={unpublishTarget !== null}
        onOpenChange={(open) => (!open ? setUnpublishTarget(null) : null)}
        title={t('Take this announcement down?')}
        desc={t(
          'It stops popping up on the website right away and shows as ended. The record stays in the announcement center history; to bring it back, edit its expiry time.'
        )}
        confirmText={t('Unpublish')}
        handleConfirm={confirmUnpublish}
      />

      <ConfirmDialog
        open={clearTarget !== null}
        onOpenChange={(open) => (!open ? setClearTarget(null) : null)}
        title={t('Clear translations')}
        desc={t(
          'Clear the title and body of {{count}} languages ({{locales}})? {{source}} stays as the source language. This only takes effect after saving.',
          {
            count: clearTarget?.dropped.length ?? 0,
            locales: clearTarget?.dropped.join(', ') ?? '',
            source: clearTarget?.source ?? '',
          }
        )}
        confirmText={t('Clear translations')}
        handleConfirm={applyClearTranslations}
      />

      <AlertDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => (!open ? setDeleteTarget(null) : null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Are you sure?')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('Announcement "{{slug}}" will be deleted permanently.', {
                slug: deleteTarget?.slug ?? '',
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction onClick={() => void confirmDelete()}>
              {t('Delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
