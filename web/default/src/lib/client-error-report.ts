import { AxiosError } from 'axios'

type ClientErrorReportPayload = {
  source: string
  route?: string
  href?: string
  name?: string
  message?: string
  stack?: string
  digest?: string
  status?: number
  request_url?: string
  method?: string
  details?: Record<string, unknown>
}

const MAX_FIELD_LEN = 500
const MAX_STACK_LEN = 4000
const RECENT_ERROR_WINDOW_MS = 30_000
const recentErrorKeys = new Map<string, number>()

function trim(value: string | null | undefined, maxLen = MAX_FIELD_LEN) {
  const normalized = (value ?? '').trim()
  if (normalized.length <= maxLen) return normalized
  return normalized.slice(0, maxLen)
}

function currentRoute() {
  if (typeof window === 'undefined') return ''
  return `${window.location.pathname}${window.location.search}`
}

function currentHref() {
  if (typeof window === 'undefined') return ''
  return window.location.href
}

function shouldReportRoute(route: string) {
  return route.startsWith('/_panel')
}

function cleanupRecentErrors(now: number) {
  for (const [key, ts] of recentErrorKeys.entries()) {
    if (now - ts > RECENT_ERROR_WINDOW_MS) {
      recentErrorKeys.delete(key)
    }
  }
}

function buildDedupeKey(payload: ClientErrorReportPayload) {
  return [
    payload.source,
    payload.route,
    payload.status ?? '',
    payload.request_url,
    payload.message,
    payload.digest,
  ].join('|')
}

function normalizePayload(
  payload: ClientErrorReportPayload
): ClientErrorReportPayload | null {
  const route = trim(payload.route || currentRoute())
  if (!shouldReportRoute(route)) return null

  const normalized: ClientErrorReportPayload = {
    source: trim(payload.source),
    route,
    href: trim(payload.href || currentHref()),
    name: trim(payload.name),
    message: trim(payload.message),
    stack: trim(payload.stack, MAX_STACK_LEN),
    digest: trim(payload.digest),
    status: payload.status,
    request_url: trim(payload.request_url),
    method: trim(payload.method, 32),
    details: payload.details,
  }

  if (!normalized.message && !normalized.stack && !normalized.request_url) {
    return null
  }
  return normalized
}

export function reportClientError(payload: ClientErrorReportPayload) {
  if (typeof window === 'undefined') return
  const normalized = normalizePayload(payload)
  if (!normalized) return

  const now = Date.now()
  cleanupRecentErrors(now)
  const dedupeKey = buildDedupeKey(normalized)
  const lastSeen = recentErrorKeys.get(dedupeKey)
  if (lastSeen && now - lastSeen < RECENT_ERROR_WINDOW_MS) {
    return
  }
  recentErrorKeys.set(dedupeKey, now)

  const body = JSON.stringify(normalized)
  void fetch('/api/user/client-error', {
    method: 'POST',
    credentials: 'include',
    keepalive: true,
    headers: {
      'Content-Type': 'application/json',
    },
    body,
  }).catch(() => {
    // Swallow reporting failures; user-facing flow must remain unaffected.
  })
}

function toRecord(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    return undefined
  return value as Record<string, unknown>
}

export function reportErrorLike(
  source: string,
  error: unknown,
  extras?: Partial<ClientErrorReportPayload>
) {
  if (error instanceof AxiosError) {
    reportClientError({
      source,
      name: error.name,
      message: error.message,
      stack: error.stack,
      status: error.response?.status,
      request_url:
        typeof error.config?.url === 'string' ? error.config.url : undefined,
      method:
        typeof error.config?.method === 'string'
          ? error.config.method.toUpperCase()
          : undefined,
      details: {
        code: error.code,
        response_message:
          typeof error.response?.data?.message === 'string'
            ? error.response.data.message
            : undefined,
      },
      ...extras,
    })
    return
  }

  if (error instanceof Error) {
    reportClientError({
      source,
      name: error.name,
      message: error.message,
      stack: error.stack,
      ...extras,
    })
    return
  }

  const maybeRecord = toRecord(error)
  reportClientError({
    source,
    name: typeof maybeRecord?.name === 'string' ? maybeRecord.name : undefined,
    message:
      typeof maybeRecord?.message === 'string'
        ? maybeRecord.message
        : trim(String(error)),
    stack:
      typeof maybeRecord?.stack === 'string' ? maybeRecord.stack : undefined,
    digest:
      typeof maybeRecord?.digest === 'string' ? maybeRecord.digest : undefined,
    details: maybeRecord,
    ...extras,
  })
}
