/*
Copyright (C) 2023-2026 QuantumNous

Video models billed as USD per second (720p base) × duration × resolution.
Runtime billing lives in Go relay/task helpers; this module is for UI labels.
*/

/** Default USD/s @ 720p (matches setting/ratio_setting/model_ratio.go). */
export const VIDEO_PER_SECOND_DEFAULT_PRICES: Record<string, number> = {
  'sora-2': 0.08,
  'sora-2-pro': 0.24,
  'minimax-h3': 0.08,
}

/** Official MiniMax-H3 API price multiplier relative to the 768P base. */
export const VIDEO_PER_SECOND_RESOLUTION_RATIOS: Record<string, Record<string, number>> = {
  'minimax-h3': { '768P': 1, '2K': 0.13 / 0.08 },
}

export function isVideoPerSecondModel(modelName: string): boolean {
  if (!modelName) return false
  return Object.prototype.hasOwnProperty.call(
    VIDEO_PER_SECOND_DEFAULT_PRICES,
    modelName.toLowerCase()
  )
}

export function getVideoPerSecondDefaultPrice(
  modelName: string
): number | undefined {
  return VIDEO_PER_SECOND_DEFAULT_PRICES[modelName.toLowerCase()]
}

export function getVideoPerSecondDetailKey(modelName: string): string {
  const model = modelName.toLowerCase()
  if (model === 'minimax-h3') return 'Video per-second detail minimax-h3'
  return model === 'sora-2-pro'
    ? 'Video per-second detail sora-2-pro'
    : 'Video per-second detail sora-2'
}
