import { createFileRoute } from '@tanstack/react-router'
import { ProviderSettings } from '@/features/provider-settings'

export const Route = createFileRoute('/_authenticated/provider-settings/')({
  component: ProviderSettings,
})
