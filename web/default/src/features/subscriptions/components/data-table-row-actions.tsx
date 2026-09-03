/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { type Row } from '@tanstack/react-table'
import {
  MoreHorizontal,
  PackageCheck,
  PackageX,
  Pencil,
  Power,
  PowerOff,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { deletePlan, patchPlanSoldOut } from '../api'
import type { PlanRecord } from '../types'
import { useSubscriptions } from './subscriptions-provider'

interface DataTableRowActionsProps {
  row: Row<PlanRecord>
}

export function DataTableRowActions({ row }: DataTableRowActionsProps) {
  const { t } = useTranslation()
  const { setOpen, setCurrentRow, triggerRefresh } = useSubscriptions()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant='ghost' className='h-8 w-8 p-0' />}
      >
        <MoreHorizontal className='h-4 w-4' />
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end'>
        <DropdownMenuItem
          onClick={() => {
            setCurrentRow(row.original)
            setOpen('update')
          }}
        >
          <Pencil className='mr-2 h-4 w-4' />
          {t('Edit')}
        </DropdownMenuItem>
        <DropdownMenuItem
          onClick={() => {
            setCurrentRow(row.original)
            setOpen('toggle-status')
          }}
        >
          {row.original.plan.enabled ? (
            <>
              <PowerOff className='mr-2 h-4 w-4' />
              {t('Disable')}
            </>
          ) : (
            <>
              <Power className='mr-2 h-4 w-4' />
              {t('Enable')}
            </>
          )}
        </DropdownMenuItem>
        {row.original.plan.plan_type === 'coding_plan' ? (
          <DropdownMenuItem
            onClick={async () => {
              const soldOut = row.original.plan.sold_out === true
              const confirmation = soldOut
                ? t('Resume sales for this plan?')
                : t('Mark this plan as sold out?')
              if (!window.confirm(confirmation)) return
              try {
                const result = await patchPlanSoldOut(
                  row.original.plan.id,
                  !soldOut
                )
                if (!result.success) throw new Error(result.message)
                toast.success(
                  soldOut ? t('Sales have resumed') : t('Marked as sold out')
                )
                triggerRefresh()
              } catch (error) {
                toast.error(
                  error instanceof Error ? error.message : t('Operation failed')
                )
              }
            }}
          >
            {row.original.plan.sold_out ? (
              <>
                <PackageCheck className='mr-2 h-4 w-4' />
                {t('Resume sales')}
              </>
            ) : (
              <>
                <PackageX className='mr-2 h-4 w-4' />
                {t('Mark as sold out')}
              </>
            )}
          </DropdownMenuItem>
        ) : null}
        {row.original.plan.plan_type === 'gpt_subscription' ? (
          <DropdownMenuItem
            className='text-destructive focus:text-destructive'
            onClick={async () => {
              if (!window.confirm(t('Delete this unreferenced plan?'))) return
              try {
                const result = await deletePlan(row.original.plan.id)
                if (!result.success) throw new Error(result.message)
                toast.success(t('Delete succeeded'))
                triggerRefresh()
              } catch (error) {
                toast.error(
                  error instanceof Error ? error.message : t('Operation failed')
                )
              }
            }}
          >
            <Trash2 className='mr-2 h-4 w-4' />
            {t('Delete')}
          </DropdownMenuItem>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
