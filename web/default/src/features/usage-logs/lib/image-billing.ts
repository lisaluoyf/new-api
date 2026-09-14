import type { LogOtherData } from '../types'

const imagePriceLabels: Record<string, string> = {
  'Image Output': 'Image output (<= 2,610,000 pixels)',
  'High-Resolution Image Output': 'Image output (> 2,610,000 pixels)',
  'Layer Image Output': 'Layer output (<= 2,610,000 pixels)',
  'High-Resolution Layer Image Output': 'Layer output (> 2,610,000 pixels)',
  'Image Input (after first)': 'Paid input images',
}

export function getImageBillingBreakdown(other: LogOtherData | null) {
  const billing = other?.image_billing
  if (
    !billing ||
    !Number.isFinite(billing.base_amount_usd) ||
    !Number.isInteger(billing.generated_images) ||
    billing.generated_images < 1 ||
    !Number.isInteger(billing.input_images) ||
    billing.input_images < 0 ||
    !billing.billable_counts ||
    !billing.base_prices
  )
    return null

  const items = []
  for (const [variant, label] of Object.entries(imagePriceLabels)) {
    const quantity = billing.billable_counts[variant] ?? 0
    if (!Number.isInteger(quantity) || quantity < 0) return null
    if (!quantity) continue
    const unitPrice = billing.base_prices[variant]
    if (!Number.isFinite(unitPrice) || unitPrice < 0) return null
    items.push({ label, quantity, unitPrice, subtotal: quantity * unitPrice })
  }
  if (!items.length) return null

  // Prices and quantities come from the settled log, never today's settings.
  const basePrice = billing.base_prices['Image Output']
  const channelRatio =
    Number.isFinite(other?.model_price) && basePrice > 0
      ? other!.model_price! / basePrice
      : null
  const userRatio = other?.user_group_ratio
  const isUserRatio =
    userRatio != null && Number.isFinite(userRatio) && userRatio !== -1
  const groupRatio = isUserRatio ? userRatio : other?.group_ratio
  const calculatedCharge =
    channelRatio != null && groupRatio != null && Number.isFinite(groupRatio)
      ? billing.base_amount_usd * channelRatio * groupRatio
      : null

  return {
    items,
    isLayers: billing.layer_decomposition,
    outputImages: billing.generated_images,
    inputImages: billing.input_images,
    freeInputImages: Math.min(1, billing.input_images),
    paidInputImages: billing.billable_counts['Image Input (after first)'] ?? 0,
    baseAmount: billing.base_amount_usd,
    channelRatio,
    groupRatio,
    calculatedCharge,
  }
}
