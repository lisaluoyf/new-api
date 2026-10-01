import { createFileRoute, redirect } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { ROLE } from '@/lib/roles'
import { PaymentReconciliationPage } from '@/features/payment-reconciliation'

export const Route = createFileRoute('/_authenticated/payment-reconciliation/')(
  {
    beforeLoad: () => {
      if (useAuthStore.getState().auth.user?.role !== ROLE.SUPER_ADMIN)
        throw redirect({ to: '/403' })
    },
    component: PaymentReconciliationPage,
  }
)
