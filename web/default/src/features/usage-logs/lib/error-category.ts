export function getErrorCategory(
  content: string,
  other?: {
    error_category?: string
    error_code?: string
    status_code?: number
  } | null
): string {
  const labels: Record<string, string> = {
    request_parameter: 'Request parameter error',
    quota_limit: 'Account or plan quota limit',
    content_policy: 'Content policy rejection',
    client_disconnect: 'Client disconnected',
    upstream_rate_limit: 'Upstream rate limit',
    upstream_failure: 'Upstream failure',
    other: 'Other error',
  }
  if (other?.error_category && labels[other.error_category])
    return labels[other.error_category]
  const text = `${other?.error_code ?? ''} ${content}`.toLowerCase()
  const status =
    other?.status_code ?? Number(content.match(/status_code=(\d+)/)?.[1] ?? 0)
  if (status === 499 || /client_cancel|client_gone|context canceled/.test(text))
    return labels.client_disconnect
  if (
    /insufficient_user_quota|pre_consume_token_quota_failed|gpt_subscription_rolling_limit|gpt pass|额度已用尽/.test(
      text
    )
  )
    return labels.quota_limit
  if (
    /content_policy|content_safety|内容审计|cybersecurity risk|prompt_blocked|sensitive_words/.test(
      text
    )
  )
    return labels.content_policy
  if (
    status === 400 ||
    /unsupported_reasoning_effort|invalid_request|does not support reasoning effort/.test(
      text
    )
  )
    return labels.request_parameter
  if (status === 429) return labels.upstream_rate_limit
  if (status >= 500) return labels.upstream_failure
  return labels.other
}
