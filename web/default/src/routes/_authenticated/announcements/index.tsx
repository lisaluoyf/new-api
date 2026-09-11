import { createFileRoute, redirect } from '@tanstack/react-router'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'
import { SiteAnnouncementsPage } from '@/features/site-announcements'

export const Route = createFileRoute('/_authenticated/announcements/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({ to: '/403' })
    }
  },
  component: SiteAnnouncementsPage,
})
