export type ModelCategoryKey =
  | 'all'
  | 'foreign'
  | 'domestic'
  | 'gpt'
  | 'claude'
  | 'gemini'
  | 'grok'
  | 'deepseek'
  | 'glm'
  | 'kimi'
  | 'qwen'
  | 'doubao'
  | 'minimax'
  | 'mimo'
  | 'kling'
  | 'embeddings'

export const MODEL_CATEGORY_FILTERS: Array<{
  key: ModelCategoryKey
  label: string
}> = [
  { key: 'all', label: 'All Models' },
  { key: 'foreign', label: 'Foreign Models' },
  { key: 'domestic', label: 'Domestic Models' },
  { key: 'gpt', label: 'GPT' },
  { key: 'claude', label: 'Claude' },
  { key: 'gemini', label: 'Gemini' },
  { key: 'grok', label: 'Grok' },
  { key: 'deepseek', label: 'DeepSeek' },
  { key: 'glm', label: 'GLM' },
  { key: 'kimi', label: 'Kimi' },
  { key: 'qwen', label: 'Qwen' },
  { key: 'doubao', label: 'Doubao' },
  { key: 'minimax', label: 'MiniMax' },
  { key: 'mimo', label: 'MiMo' },
  { key: 'kling', label: 'Kling' },
  { key: 'embeddings', label: 'Text Embeddings' },
]

const FOREIGN_MODEL_PREFIXES = ['gpt-', 'claude-', 'gemini-', 'grok-', 'sora-']

const DOMESTIC_MODEL_PREFIXES = [
  'deepseek-',
  'glm-',
  'kimi-',
  'qwen',
  'doubao-',
  'seedance-',
  'minimax-',
  'mimo-',
  'kling-',
]

function hasAnyPrefix(modelId: string, prefixes: string[]): boolean {
  return prefixes.some((prefix) => modelId.startsWith(prefix))
}

export function modelMatchesCategory(
  modelId: string,
  category: ModelCategoryKey
): boolean {
  if (category === 'all') return true
  if (category === 'embeddings')
    return modelId === 'text-embedding-3-small' || modelId === 'bge-m3'
  if (category === 'foreign')
    return hasAnyPrefix(modelId, FOREIGN_MODEL_PREFIXES)
  if (category === 'domestic') {
    return hasAnyPrefix(modelId, DOMESTIC_MODEL_PREFIXES)
  }
  if (category === 'gpt') return modelId.startsWith('gpt-')
  if (category === 'claude') return modelId.startsWith('claude-')
  if (category === 'gemini') return modelId.startsWith('gemini-')
  if (category === 'grok') return modelId.startsWith('grok-')
  if (category === 'deepseek') return modelId.startsWith('deepseek-')
  if (category === 'glm') return modelId.startsWith('glm-')
  if (category === 'kimi') return modelId.startsWith('kimi-')
  if (category === 'qwen') return modelId.startsWith('qwen')
  if (category === 'doubao')
    return modelId.startsWith('doubao-') || modelId.startsWith('seedance-')
  if (category === 'minimax') return modelId.startsWith('minimax-')
  if (category === 'mimo') return modelId.startsWith('mimo-')
  if (category === 'kling') return modelId.startsWith('kling-')
  return true
}

const NON_LLM_MODEL_IDS = new Set([
  'text-embedding-3-small',
  'bge-m3',
  'jev-latest',
  'jev-preview',
  'jev-1.13.0',
  'grok-imagine-image-2.0',
  'doubao-seedream-5-0-pro-260628',
  'midjourney-v8.2',
  'midjourney-niji-7',
  'gemini-2.5-flash-image',
  'gemini-3-pro-image',
  'gemini-3.1-flash-image',
  'gemini-3.1-flash-image-preview',
  'gemini-nano-banana-2.1',
  'gpt-image-2',
  'gpt-image-2.5-sunburst',
  'gpt-image-2.5-flare',
  'gpt-image-2-fd',
  'seedance-2.0',
  'doubao-seedance-2.0',
  'seedance-2.5',
  'kling-v3-motion-control',
  'grok-imagine-video-1.5',
])

const VIDEO_MODEL_IDS = new Set([
  'seedance-2.0',
  'doubao-seedance-2.0',
  'seedance-2.5',
  'kling-v3-motion-control',
  'grok-imagine-video-1.5',
])

const IMAGE_MODEL_IDS = new Set([
  'grok-imagine-image-2.0',
  'doubao-seedream-5-0-pro-260628',
  'gemini-2.5-flash-image',
  'gemini-3-pro-image',
  'gemini-3.1-flash-image',
  'gemini-3.1-flash-image-preview',
  'gemini-nano-banana-2.1',
  'gpt-image-2',
  'gpt-image-2.5-sunburst',
  'gpt-image-2.5-flare',
  'gpt-image-2-fd',
])

export function isLLMModel(modelId: string): boolean {
  if (modelId === 'apimaster-freemodel') return false
  return !NON_LLM_MODEL_IDS.has(modelId)
}

export function getPriceUnit(modelId: string): string {
  if (modelId === 'midjourney-v8.2' || modelId === 'midjourney-niji-7')
    return '$/generation'
  if (VIDEO_MODEL_IDS.has(modelId)) return '$/s'
  if (IMAGE_MODEL_IDS.has(modelId)) return '$/req'
  return '$/1M'
}
