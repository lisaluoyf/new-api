type CachePricingData = {
  name: string
  ratio?: string
  createCacheRatio?: string
  createCache1hRatio?: string
}

function numberOrUndefined(raw?: string) {
  if (raw === undefined || raw.trim() === '') return undefined
  const value = Number(raw)
  return Number.isFinite(value) && value >= 0 ? value : undefined
}

export function cacheWriteBasePrices(data: CachePricingData) {
  const ratio = numberOrUndefined(data.ratio)
  const fiveMinuteRatio = numberOrUndefined(data.createCacheRatio)
  const oneHourRatio = numberOrUndefined(data.createCache1hRatio)
  const inputPrice = ratio === undefined ? undefined : ratio * 2
  const fiveMinutes =
    inputPrice === undefined || fiveMinuteRatio === undefined
      ? undefined
      : inputPrice * fiveMinuteRatio
  const inheritsOneHour =
    oneHourRatio === undefined &&
    data.name.toLowerCase().includes('claude') &&
    fiveMinutes !== undefined
  const oneHour =
    inputPrice !== undefined && oneHourRatio !== undefined
      ? inputPrice * oneHourRatio
      : inheritsOneHour
        ? fiveMinutes! * 1.6
        : undefined
  return { fiveMinutes, oneHour, inheritsOneHour }
}
