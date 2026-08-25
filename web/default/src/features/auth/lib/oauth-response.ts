type OAuthResponse = {
  success?: boolean
  message?: string
  data?: {
    action?: string
  } | null
}

export function isOAuthBindSuccessResponse(payload: unknown): boolean {
  if (!payload || typeof payload !== 'object') return false

  const response = payload as OAuthResponse
  return (
    response.success === true &&
    (response.data?.action === 'bind' || response.message === 'bind')
  )
}
