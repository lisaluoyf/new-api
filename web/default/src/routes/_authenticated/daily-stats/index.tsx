import { createFileRoute, redirect } from '@tanstack/react-router'
import { DailyStatsPage } from '@/features/daily-stats'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/daily-stats/')({
  beforeLoad: () => {
    const auth = useAuthStore.getState().auth
    if (auth.user?.role !== ROLE.SUPER_ADMIN) {
      throw redirect({ to: '/403' })
    }
  },
  component: DailyStatsPage,
})
