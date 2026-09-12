interface SortableChannelData {
  status: number
  model_enabled: boolean
  user_price?: number | null
  free_model_config?: {
    enabled?: boolean
    priority?: number
    weight?: number
  } | null
}

export function sortChannelData<T extends SortableChannelData>(
  rows: readonly T[],
  model: string
): T[] {
  return [...rows].sort((a, b) => {
    if (model === 'apimaster-freemodel') {
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
    const aPriced = a.user_price != null && a.user_price > 0
    const bPriced = b.user_price != null && b.user_price > 0
    if (aPriced !== bPriced) return aPriced ? -1 : 1
    return (a.user_price ?? Infinity) - (b.user_price ?? Infinity)
  })
}
