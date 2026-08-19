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
import { useState } from 'react'
import { type Row } from '@tanstack/react-table'
import {
  MoreHorizontal,
  Pencil,
  Trash2,
  Power,
  PowerOff,
  ArrowUp,
  ArrowDown,
  KeyRound,
  ShieldAlert,
  Link2,
  CreditCard,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { UserSubscriptionsDialog } from '@/features/subscriptions/components/dialogs/user-subscriptions-dialog'
import {
  addTrialBlockedEmailDomain,
  invalidateUserGPTSubscription,
  manageUser,
  removeTrialBlockedEmailDomain,
  resetUserPasskey,
  resetUserTwoFA,
} from '../api'
import {
  USER_STATUS,
  USER_ROLE,
  ERROR_MESSAGES,
  isUserDeleted,
} from '../constants'
import { getUserActionMessage } from '../lib'
import { type User, type ManageUserAction } from '../types'
import { UserBindingDialog } from './dialogs/user-binding-dialog'
import { useUsers } from './users-provider'

interface DataTableRowActionsProps {
  row: Row<User>
}

export function DataTableRowActions({ row }: DataTableRowActionsProps) {
  const { t } = useTranslation()
  const user = row.original
  const { setOpen, setCurrentRow, triggerRefresh } = useUsers()
  const [resetPasskeyOpen, setResetPasskeyOpen] = useState(false)
  const [resetTwoFAOpen, setResetTwoFAOpen] = useState(false)
  const [addTrialBlockedDomainOpen, setAddTrialBlockedDomainOpen] =
    useState(false)
  const [removeTrialBlockedDomainOpen, setRemoveTrialBlockedDomainOpen] =
    useState(false)
  const [bindingDialogOpen, setBindingDialogOpen] = useState(false)
  const [subscriptionsDialogOpen, setSubscriptionsDialogOpen] = useState(false)
  const [closeGPTSubscriptionOpen, setCloseGPTSubscriptionOpen] =
    useState(false)
  const [closingGPTSubscription, setClosingGPTSubscription] = useState(false)

  const handleEdit = () => {
    setCurrentRow(user)
    setOpen('update')
  }

  const handleDelete = () => {
    setCurrentRow(user)
    setOpen('delete')
  }

  const handleManage = async (action: Exclude<ManageUserAction, 'delete'>) => {
    try {
      const result = await manageUser(user.id, action)
      if (result.success) {
        toast.success(t(getUserActionMessage(action)))
        triggerRefresh()
      } else {
        toast.error(
          result.message || t('Failed to {{action}} user', { action })
        )
      }
    } catch (_error) {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    }
  }

  const handleResetPasskey = async () => {
    try {
      const result = await resetUserPasskey(user.id)
      if (result.success) {
        toast.success(t('Passkey reset successfully'))
        triggerRefresh()
      } else {
        toast.error(result.message || t('Failed to reset Passkey'))
      }
    } catch (_error) {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setResetPasskeyOpen(false)
    }
  }

  const handleResetTwoFA = async () => {
    try {
      const result = await resetUserTwoFA(user.id)
      if (result.success) {
        toast.success(t('Two-factor authentication reset'))
        triggerRefresh()
      } else {
        toast.error(result.message || t('Failed to reset 2FA'))
      }
    } catch (_error) {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setResetTwoFAOpen(false)
    }
  }

  const emailDomain = user.email?.split('@')[1]?.trim().toLowerCase() ?? ''

  const handleAddTrialBlockedDomain = async () => {
    try {
      const result = await addTrialBlockedEmailDomain(user.id)
      if (result.success) {
        toast.success(
          `已将 ${result.data?.domain || emailDomain} 加入 GPT Trial 黑域名`
        )
        triggerRefresh()
      } else {
        toast.error(result.message || '增加 GPT Trial 黑域名失败')
      }
    } catch (_error) {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setAddTrialBlockedDomainOpen(false)
    }
  }

  const handleRemoveTrialBlockedDomain = async () => {
    try {
      const result = await removeTrialBlockedEmailDomain(user.id)
      if (result.success) {
        toast.success(
          `已将 ${result.data?.domain || emailDomain} 移出 GPT Trial 黑域名`
        )
        triggerRefresh()
      } else {
        toast.error(result.message || '移出 GPT Trial 黑域名失败')
      }
    } catch (_error) {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setRemoveTrialBlockedDomainOpen(false)
    }
  }

  const handleCloseGPTSubscription = async () => {
    setClosingGPTSubscription(true)
    try {
      const result = await invalidateUserGPTSubscription(user.id)
      if (result.success) {
        toast.success(result.data?.message || t('Has been invalidated'))
        triggerRefresh()
      } else {
        toast.error(result.message || t('Failed to close GPT subscription'))
      }
    } catch (_error) {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setClosingGPTSubscription(false)
      setCloseGPTSubscriptionOpen(false)
    }
  }

  const isDisabled = user.status === USER_STATUS.DISABLED
  const isAdmin = user.role >= USER_ROLE.ADMIN
  const isRoot = user.role === USER_ROLE.ROOT
  const isTopupForbidden = user.topup_forbidden === true
  const hasActiveGPTSubscription = user.gpt_subscription_status === 'active'

  if (isUserDeleted(user)) {
    return null
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant='ghost'
              className='data-popup-open:bg-muted flex h-8 w-8 p-0'
            />
          }
        >
          <MoreHorizontal className='h-4 w-4' />
          <span className='sr-only'>{t('Open menu')}</span>
        </DropdownMenuTrigger>
        <DropdownMenuContent align='end' className='w-[220px]'>
          <DropdownMenuItem onClick={handleEdit}>
            {t('Edit')}
            <DropdownMenuShortcut>
              <Pencil size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          <DropdownMenuSeparator />

          {isDisabled ? (
            <DropdownMenuItem onClick={() => handleManage('enable')}>
              {t('Enable')}
              <DropdownMenuShortcut>
                <Power size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          ) : (
            <DropdownMenuItem
              onClick={() => handleManage('disable')}
              disabled={isRoot}
            >
              {t('Disable')}
              <DropdownMenuShortcut>
                <PowerOff size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}

          {isAdmin && !isRoot && (
            <DropdownMenuItem onClick={() => handleManage('demote')}>
              {t('Demote')}
              <DropdownMenuShortcut>
                <ArrowDown size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}

          {!isAdmin && (
            <DropdownMenuItem onClick={() => handleManage('promote')}>
              {t('Promote')}
              <DropdownMenuShortcut>
                <ArrowUp size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}

          <DropdownMenuItem
            onSelect={(event) => {
              event.preventDefault()
              setBindingDialogOpen(true)
            }}
          >
            {t('Manage Bindings')}
            <DropdownMenuShortcut>
              <Link2 size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          <DropdownMenuItem
            onSelect={(event) => {
              event.preventDefault()
              setSubscriptionsDialogOpen(true)
            }}
          >
            {t('Manage Subscriptions')}
            <DropdownMenuShortcut>
              <CreditCard size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          {hasActiveGPTSubscription && (
            <DropdownMenuItem
              onSelect={(event) => {
                event.preventDefault()
                setCloseGPTSubscriptionOpen(true)
              }}
              disabled={isRoot}
              className='text-destructive focus:text-destructive'
            >
              {t('Close GPT subscription')}
              <DropdownMenuShortcut>
                <PowerOff size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}

          <DropdownMenuItem
            onClick={() =>
              handleManage(isTopupForbidden ? 'allow_topup' : 'forbid_topup')
            }
            disabled={isRoot}
          >
            {t(isTopupForbidden ? 'Allow top-up' : 'Forbid top-up')}
            <DropdownMenuShortcut>
              <CreditCard size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          {emailDomain && (
            <>
              <DropdownMenuItem
                onSelect={(event) => {
                  event.preventDefault()
                  setAddTrialBlockedDomainOpen(true)
                }}
              >
                增加GPTTrial黑域名
                <DropdownMenuShortcut>
                  <ShieldAlert size={16} />
                </DropdownMenuShortcut>
              </DropdownMenuItem>

              <DropdownMenuItem
                onSelect={(event) => {
                  event.preventDefault()
                  setRemoveTrialBlockedDomainOpen(true)
                }}
              >
                移出GPTTrial黑域名
                <DropdownMenuShortcut>
                  <Trash2 size={16} />
                </DropdownMenuShortcut>
              </DropdownMenuItem>
            </>
          )}

          <DropdownMenuSeparator />

          <DropdownMenuItem
            onSelect={(event) => {
              event.preventDefault()
              setResetPasskeyOpen(true)
            }}
            disabled={isRoot}
          >
            {t('Reset Passkey')}
            <DropdownMenuShortcut>
              <KeyRound size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          <DropdownMenuItem
            onSelect={(event) => {
              event.preventDefault()
              setResetTwoFAOpen(true)
            }}
            disabled={isRoot}
          >
            {t('Reset 2FA')}
            <DropdownMenuShortcut>
              <ShieldAlert size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          <DropdownMenuSeparator />

          <DropdownMenuItem
            onClick={handleDelete}
            className='text-destructive focus:text-destructive'
            disabled={isRoot}
          >
            {t('Delete')}
            <DropdownMenuShortcut>
              <Trash2 size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <ConfirmDialog
        open={resetPasskeyOpen}
        onOpenChange={setResetPasskeyOpen}
        title={t('Reset Passkey')}
        desc={`Reset Passkey for ${user.username}? The user will need to register a new Passkey before using passwordless login.`}
        confirmText='Reset Passkey'
        handleConfirm={handleResetPasskey}
      />

      <ConfirmDialog
        open={resetTwoFAOpen}
        onOpenChange={setResetTwoFAOpen}
        title={t('Reset Two-Factor Authentication')}
        desc={`Reset 2FA for ${user.username}? The user must set up 2FA again to continue using it.`}
        confirmText='Reset 2FA'
        handleConfirm={handleResetTwoFA}
      />

      <ConfirmDialog
        open={addTrialBlockedDomainOpen}
        onOpenChange={setAddTrialBlockedDomainOpen}
        title='增加 GPT Trial 黑域名'
        desc={`将 ${emailDomain || '该域名'} 加入 GPT Trial 黑域名后，这个域名下的账号将无法领取 GPT Trial。`}
        confirmText='确认增加'
        handleConfirm={handleAddTrialBlockedDomain}
      />

      <ConfirmDialog
        open={removeTrialBlockedDomainOpen}
        onOpenChange={setRemoveTrialBlockedDomainOpen}
        title='移出 GPT Trial 黑域名'
        desc={`将 ${emailDomain || '该域名'} 移出 GPT Trial 黑域名后，这个域名下的账号可重新按现有规则参与领取判定。`}
        confirmText='确认移出'
        handleConfirm={handleRemoveTrialBlockedDomain}
      />

      <ConfirmDialog
        open={closeGPTSubscriptionOpen}
        onOpenChange={setCloseGPTSubscriptionOpen}
        title={t('Close GPT subscription')}
        desc={t(
          'Close GPT subscription for {{username}}? The subscription will end immediately. Historical usage and billing records will not be changed.',
          { username: user.username }
        )}
        confirmText={t('Close subscription')}
        destructive
        isLoading={closingGPTSubscription}
        handleConfirm={handleCloseGPTSubscription}
      />

      <UserBindingDialog
        open={bindingDialogOpen}
        onOpenChange={setBindingDialogOpen}
        userId={user.id}
        onUnbindSuccess={triggerRefresh}
      />

      <UserSubscriptionsDialog
        open={subscriptionsDialogOpen}
        onOpenChange={setSubscriptionsDialogOpen}
        user={{ id: user.id, username: user.username }}
        onSuccess={triggerRefresh}
      />
    </>
  )
}
