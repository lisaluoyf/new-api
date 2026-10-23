export const OMNI_MODEL = 'gemini-omni-1.1-flash'
export const OMNI_OPTION = 'GeminiOmniPricing'
export const OMNI_TIERS = ['Official', 'Ext'].flatMap((kind) =>
  ['360P', '720P', '1080P', '4K'].flatMap((res) =>
    kind === 'Official'
      ? [`Official-${res}`]
      : [4, 6, 8, 10]
          .map((seconds) => `Ext-${res}-${seconds}s`)
          .concat(`Ext-${res}-reference`)
  )
)
function record(raw: string | undefined): Record<string, unknown> {
  const value: unknown = JSON.parse(raw || '{}')
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Invalid pricing configuration')
  return value as Record<string, unknown>
}
export function parseOmniPrices(
  raw: string | undefined
): Record<string, number> {
  const value = record(raw)
  if (Object.keys(value).length !== OMNI_TIERS.length)
    throw new Error('All 24 Omni prices are required')
  return Object.fromEntries(
    OMNI_TIERS.map((key) => {
      const price = value[key]
      if (typeof price !== 'number' || !Number.isFinite(price) || price <= 0)
        throw new Error('Invalid Omni price')
      return [key, price]
    })
  )
}
export function mergeOmniBasePrice(
  modelRaw: string,
  videoRaw: string,
  base: number
) {
  if (!Number.isFinite(base) || base <= 0)
    throw new Error('Invalid Omni base price')
  const models = record(modelRaw)
  const videos = record(videoRaw)
  const previous = videos[OMNI_MODEL]
  if (
    previous != null &&
    (typeof previous !== 'object' || Array.isArray(previous))
  )
    throw new Error('Invalid pricing configuration')
  const config = (previous ?? {}) as Record<string, unknown>
  models[OMNI_MODEL] = base
  videos[OMNI_MODEL] = {
    ...config,
    unit: 'second',
    base_variant: '720P',
    base_price: base,
    prices: { ...record(JSON.stringify(config.prices ?? {})), '720P': base },
  }
  return {
    ModelPrice: JSON.stringify(models),
    VideoModelPricing: JSON.stringify(videos),
  }
}
