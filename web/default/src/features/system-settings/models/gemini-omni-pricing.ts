export const OMNI_MODEL = 'gemini-omni-1.1-flash'
export const OMNI_RESOLUTIONS = ['360P', '720P', '1080P', '4K'] as const
export const OMNI_TOKENS_PER_SECOND = {
  '360P': 1931,
  '720P': 5792,
  '1080P': 8688,
  '4K': 17376,
}
export const OMNI_OFFICIAL_PRICES: Record<string, number> = {
  '360P': 0.0337925,
  '720P': 0.10136,
  '1080P': 0.15204,
  '4K': 0.30408,
}
function record(raw: string | undefined): Record<string, unknown> {
  const value: unknown = JSON.parse(raw || '{}')
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Invalid pricing configuration')
  return value as Record<string, unknown>
}
export function readOmniPrices(videoRaw: string): Record<string, string> {
  const videos = record(videoRaw)
  const existing = videos[OMNI_MODEL]
  if (
    existing != null &&
    (typeof existing !== 'object' || Array.isArray(existing))
  )
    throw new Error('Invalid pricing configuration')
  const config = (existing ?? {}) as Record<string, unknown>
  const prices = record(JSON.stringify(config.prices ?? {}))
  return Object.fromEntries(
    OMNI_RESOLUTIONS.map((key) => [
      key,
      String(prices[key] ?? OMNI_OFFICIAL_PRICES[key]),
    ])
  )
}
export function mergeOmniPrices(
  modelRaw: string,
  videoRaw: string,
  values: Record<string, string>
) {
  const models = record(modelRaw)
  const videos = record(videoRaw)
  const prices = Object.fromEntries(
    OMNI_RESOLUTIONS.map((key) => {
      const value = Number(values[key])
      if (!values[key]?.trim() || !Number.isFinite(value) || value <= 0)
        throw new Error('All four official prices must be positive')
      return [key, value]
    })
  )
  const existing = videos[OMNI_MODEL]
  if (
    existing != null &&
    (typeof existing !== 'object' || Array.isArray(existing))
  )
    throw new Error('Invalid pricing configuration')
  models[OMNI_MODEL] = prices['720P']
  videos[OMNI_MODEL] = {
    ...((existing as Record<string, unknown>) ?? {}),
    unit: 'second',
    base_variant: '720P',
    base_price: prices['720P'],
    prices,
    official_prices: { ...prices },
  }
  return {
    ModelPrice: JSON.stringify(models),
    VideoModelPricing: JSON.stringify(videos),
  }
}
