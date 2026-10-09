import { api } from '@/lib/api'
import type { ProviderSettingsValues } from './lib/schema'

type Response<Data> = { success: boolean; message?: string; data: Data }
export type ProviderSettingsData = ProviderSettingsValues & { models: string[] }
export type ProviderChannel = {
  channel_id: number
  user_price: number | null
  actual_output_user_price: number | null
  official_input_price: number | null
  client_exclusive: string
  status: number
  media_pricing?: { unit: string }
}

export async function getProviderSettings(): Promise<ProviderSettingsData> {
  const response = await api.get<Response<ProviderSettingsData>>(
    '/api/user/provider-settings'
  )
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function saveProviderSettings(
  values: ProviderSettingsValues
): Promise<ProviderSettingsValues> {
  const response = await api.put<Response<ProviderSettingsValues>>(
    '/api/user/provider-settings',
    values
  )
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function getProviderChannels(
  model: string
): Promise<ProviderChannel[]> {
  const response = await api.get<Response<ProviderChannel[]>>(
    '/api/user/provider-settings/channels',
    { params: { model } }
  )
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}
