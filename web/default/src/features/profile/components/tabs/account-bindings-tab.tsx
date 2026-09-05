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
import { useEffect, useMemo, useState, useCallback } from 'react'
import { Mail, Shield, Send, Link2, Unlink } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { SiGithub, SiWechat, SiLinux, SiX } from 'react-icons/si'
import { toast } from 'sonner'
import { IconDiscord } from '@/assets/brand-icons'
import {
  handleGitHubOAuth,
  handleOIDCOAuth,
  handleDiscordOAuth,
  handleLinuxDOOAuth,
} from '@/lib/oauth'
import { useDialogs } from '@/hooks/use-dialog'
import { useStatus } from '@/hooks/use-status'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { StatusBadge } from '@/components/status-badge'
import { OAUTH_BIND_STORAGE_KEY } from '@/features/auth/constants'
import {
  getApimasterBindings,
  getDiscordGroupStatus,
  getTelegramGroupStatus,
  getSelfOAuthBindings,
  startTelegramGroupVerification,
  startDiscordGroupVerification,
  unbindTelegramGroupVerification,
  unbindApimasterBinding,
  unbindCustomOAuth,
  type ApimasterTwitterBinding,
  type CustomOAuthBinding,
  type DiscordGroupStatus,
  type TelegramGroupStatus,
} from '../../api'
import type { UserProfile, BindingItem } from '../../types'
import { EmailBindDialog } from '../dialogs/email-bind-dialog'
import { WeChatBindDialog } from '../dialogs/wechat-bind-dialog'

// ============================================================================
// Account Bindings Tab Component
// ============================================================================

interface AccountBindingsTabProps {
  profile: UserProfile | null
  onUpdate: () => void
}

type DialogKey = 'email' | 'wechat'
type ActionTarget = 'twitter' | 'discord'
type AccountBindingItem = BindingItem & {
  onUnbind?: () => void
  busy?: boolean
}

export function AccountBindingsTab({
  profile,
  onUpdate,
}: AccountBindingsTabProps) {
  const { t } = useTranslation()
  const dialogs = useDialogs<DialogKey>()
  const { status, loading } = useStatus()
  const [customBindings, setCustomBindings] = useState<CustomOAuthBinding[]>([])
  const [twitterBinding, setTwitterBinding] =
    useState<ApimasterTwitterBinding | null>(null)
  const [unbindTarget, setUnbindTarget] = useState<CustomOAuthBinding | null>(
    null
  )
  const [unbinding, setUnbinding] = useState(false)
  const [pendingAction, setPendingAction] = useState<ActionTarget | null>(null)
  const [telegramGroupStatus, setTelegramGroupStatus] =
    useState<TelegramGroupStatus | null>(null)
  const [telegramStarting, setTelegramStarting] = useState(false)
  const [telegramUnbindOpen, setTelegramUnbindOpen] = useState(false)
  const [telegramUnbinding, setTelegramUnbinding] = useState(false)
  const [discordGroupStatus, setDiscordGroupStatus] =
    useState<DiscordGroupStatus | null>(null)
  const [discordChecking, setDiscordChecking] = useState(false)
  const [discordPolling, setDiscordPolling] = useState(false)

  const customProviders = status?.custom_oauth_providers as
    | Array<{ id: string; name: string }>
    | undefined

  const fetchApimasterBindings = useCallback(async () => {
    try {
      const res = await getApimasterBindings()
      if (res.success && res.data) {
        setTwitterBinding(res.data.twitter ?? null)
      }
    } catch {
      setTwitterBinding(null)
    }
  }, [])

  const fetchCustomBindings = useCallback(async () => {
    if (!customProviders || customProviders.length === 0) return
    try {
      const res = await getSelfOAuthBindings()
      if (res.success && res.data) {
        setCustomBindings(res.data)
      }
    } catch {
      // ignore
    }
  }, [customProviders])

  useEffect(() => {
    fetchCustomBindings()
  }, [fetchCustomBindings])

  useEffect(() => {
    void fetchApimasterBindings()
  }, [fetchApimasterBindings])

  const fetchTelegramGroupStatus =
    useCallback(async (): Promise<TelegramGroupStatus | null> => {
      if (!status?.telegram_group_enabled) {
        setTelegramGroupStatus(null)
        return null
      }
      try {
        const res = await getTelegramGroupStatus()
        const data = res.success ? (res.data ?? null) : null
        setTelegramGroupStatus(data)
        return data
      } catch {
        return null
      }
    }, [status?.telegram_group_enabled])

  useEffect(() => {
    void fetchTelegramGroupStatus()
  }, [fetchTelegramGroupStatus])

  const fetchDiscordGroupStatus =
    useCallback(async (): Promise<DiscordGroupStatus | null> => {
      const discordId = profile?.discord_id
      if (
        !status?.discord_oauth &&
        !status?.discord_group_enabled &&
        !discordId
      ) {
        setDiscordGroupStatus(null)
        return null
      }
      setDiscordChecking(true)
      try {
        const res = await getDiscordGroupStatus()
        const data = res.success ? (res.data ?? null) : null
        setDiscordGroupStatus(data)
        if (data?.joined || data?.status === 'service_unavailable') {
          setDiscordPolling(false)
        }
        return data
      } catch {
        setDiscordGroupStatus((current) =>
          current
            ? { ...current, status: 'service_unavailable' }
            : {
                configured: false,
                bound: Boolean(discordId),
                joined: false,
                status: 'service_unavailable',
              }
        )
        setDiscordPolling(false)
        return null
      } finally {
        setDiscordChecking(false)
      }
    }, [
      profile?.discord_id,
      status?.discord_group_enabled,
      status?.discord_oauth,
    ])

  useEffect(() => {
    void fetchDiscordGroupStatus()
  }, [fetchDiscordGroupStatus])

  useEffect(() => {
    const shouldPoll =
      telegramGroupStatus?.status === 'waiting_for_bot' ||
      (telegramGroupStatus?.identified && !telegramGroupStatus.joined)
    if (!shouldPoll) return

    const timer = window.setInterval(() => {
      void fetchTelegramGroupStatus()
    }, 5000)
    return () => window.clearInterval(timer)
  }, [fetchTelegramGroupStatus, telegramGroupStatus])

  useEffect(() => {
    if (!discordPolling || discordGroupStatus?.joined) return
    const timer = window.setInterval(() => {
      void fetchDiscordGroupStatus()
    }, 5000)
    return () => window.clearInterval(timer)
  }, [discordGroupStatus?.joined, discordPolling, fetchDiscordGroupStatus])

  useEffect(() => {
    if (!discordPolling) return
    const handleVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        void fetchDiscordGroupStatus()
      }
    }
    document.addEventListener('visibilitychange', handleVisibilityChange)
    return () =>
      document.removeEventListener('visibilitychange', handleVisibilityChange)
  }, [discordPolling, fetchDiscordGroupStatus])

  const handleUnbindCustom = async () => {
    if (!unbindTarget) return
    setUnbinding(true)
    try {
      const res = await unbindCustomOAuth(unbindTarget.provider_id)
      if (res.success) {
        toast.success(
          t('Unbound {{provider}}', {
            provider: unbindTarget.provider_name,
          })
        )
        await fetchCustomBindings()
        onUpdate()
      } else {
        toast.error(res.message || t('Unbind failed'))
      }
    } catch {
      toast.error(t('Unbind failed'))
    } finally {
      setUnbinding(false)
      setUnbindTarget(null)
    }
  }

  const handleBindCustomOAuth = (provider: { id: string; name: string }) => {
    const redirectUrl = `${window.location.origin}/oauth/${provider.id}?bind=true`
    window.location.href = `/api/oauth/${provider.id}?redirect=${encodeURIComponent(redirectUrl)}`
  }

  const handleStartTwitterBind = useCallback(() => {
    setPendingAction('twitter')
    const popup = window.open(
      '/api/auth/twitter?bind=1&popup=1',
      '_blank',
      'popup=yes,width=640,height=760,resizable=yes,scrollbars=yes'
    )
    if (!popup) {
      setPendingAction(null)
      toast.error(t('Please allow popups and try again.'))
      return
    }
    window.setTimeout(() => {
      setPendingAction((current) => (current === 'twitter' ? null : current))
    }, 60_000)
  }, [t])

  const handleUnbindTwitter = useCallback(async () => {
    setPendingAction('twitter')
    try {
      const res = await unbindApimasterBinding('twitter')
      if (res.success) {
        toast.success(t('Unbound {{provider}}', { provider: 'Twitter' }))
        await fetchApimasterBindings()
      } else {
        toast.error(res.message || t('Unbind failed'))
      }
    } catch {
      toast.error(t('Unbind failed'))
    } finally {
      setPendingAction(null)
    }
  }, [fetchApimasterBindings, t])

  const handleStartDiscordBind = useCallback(async () => {
    if (!status?.discord_client_id) {
      toast.error(t('Failed to start Discord login'))
      return
    }
    setPendingAction('discord')
    try {
      await handleDiscordOAuth(status.discord_client_id)
      window.setTimeout(() => {
        setPendingAction((current) => (current === 'discord' ? null : current))
      }, 60_000)
    } catch {
      setPendingAction(null)
      toast.error(t('Failed to start Discord login'))
    }
  }, [status?.discord_client_id, t])

  const handleDiscordCommunity = useCallback(async () => {
    const discordWindow = window.open('about:blank', '_blank')
    if (!discordWindow) {
      toast.error(t('Please allow popups and try again.'))
      return
    }
    discordWindow.opener = null
    setDiscordChecking(true)
    try {
      const res = await startDiscordGroupVerification()
      if (!res.success || !res.data?.invite_url) {
        throw new Error(
          res.message || t('Unable to start Discord verification')
        )
      }
      discordWindow.location.replace(res.data.invite_url)
      setDiscordPolling(true)
      void fetchDiscordGroupStatus()
    } catch {
      discordWindow.close()
      toast.error(t('Unable to start Discord verification'))
    } finally {
      setDiscordChecking(false)
    }
  }, [fetchDiscordGroupStatus, t])

  const handleTelegramCommunity = useCallback(async () => {
    const telegramWindow = window.open('about:blank', '_blank')
    if (!telegramWindow) {
      toast.error(t('Please allow popups and try again.'))
      return
    }
    telegramWindow.opener = null
    setTelegramStarting(true)
    try {
      const res = await startTelegramGroupVerification()
      const targetUrl = res.data?.identified
        ? res.data.group_url
        : res.data?.bot_url
      if (!res.success || !targetUrl) {
        throw new Error(
          res.message || t('Unable to start Telegram verification')
        )
      }
      telegramWindow.location.replace(targetUrl)
      void fetchTelegramGroupStatus()
    } catch {
      telegramWindow.close()
      toast.error(t('Unable to start Telegram verification'))
    } finally {
      setTelegramStarting(false)
    }
  }, [fetchTelegramGroupStatus, t])

  const handleUnbindTelegram = useCallback(async () => {
    setTelegramUnbinding(true)
    try {
      const res = await unbindTelegramGroupVerification()
      if (!res.success) {
        throw new Error(res.message || t('Unbind failed'))
      }
      setTelegramGroupStatus({
        configured: true,
        identified: false,
        joined: false,
        status: 'not_started',
      })
      toast.success(t('Unbound {{provider}}', { provider: 'Telegram' }))
      onUpdate()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Unbind failed'))
    } finally {
      setTelegramUnbinding(false)
      setTelegramUnbindOpen(false)
    }
  }, [onUpdate, t])

  useEffect(() => {
    if (typeof window === 'undefined') return

    const handleStorage = (event: StorageEvent) => {
      if (event.key !== OAUTH_BIND_STORAGE_KEY || !event.newValue) return
      try {
        const payload = JSON.parse(event.newValue) as {
          status?: string
          provider?: string
          timestamp?: number
          message?: string
        }
        if (payload?.status === 'success') {
          setPendingAction(null)
          void fetchApimasterBindings()
          void fetchCustomBindings()
          onUpdate()
          if (
            payload.provider === 'twitter' ||
            payload.provider === 'discord'
          ) {
            toast.success(t('Binding successful!'))
          }
          if (payload.provider === 'discord') {
            setDiscordPolling(true)
            void fetchDiscordGroupStatus()
          }
        } else if (payload?.status === 'error') {
          setPendingAction(null)
          toast.error(payload.message || t('Binding failed'))
        }
      } catch {
        // ignore malformed payloads
      }
      try {
        window.localStorage.removeItem(OAUTH_BIND_STORAGE_KEY)
      } catch {
        // ignore cleanup failure
      }
    }

    window.addEventListener('storage', handleStorage)
    return () => window.removeEventListener('storage', handleStorage)
  }, [
    fetchApimasterBindings,
    fetchCustomBindings,
    fetchDiscordGroupStatus,
    onUpdate,
    t,
  ])

  // Memoize bindings to prevent unnecessary recalculations
  const bindings: AccountBindingItem[] = useMemo(() => {
    if (!profile || !status) return []

    return [
      {
        id: 'email',
        label: t('Email'),
        icon: Mail,
        value: profile.email,
        isBound: Boolean(profile.email),
        isEnabled: true,
        onBind: () => dialogs.open('email'),
      },
      {
        id: 'wechat',
        label: t('WeChat'),
        icon: SiWechat as React.ComponentType<{ className?: string }>,
        value: undefined,
        isBound: Boolean(
          (profile as unknown as Record<string, unknown>).wechat_id
        ),
        isEnabled: status?.wechat_login || false,
        onBind: () => dialogs.open('wechat'),
      },
      {
        id: 'github',
        label: t('GitHub'),
        icon: SiGithub,
        value: (profile as unknown as Record<string, unknown>).github_id as
          | string
          | undefined,
        isBound: Boolean(
          (profile as unknown as Record<string, unknown>).github_id
        ),
        isEnabled: status?.github_oauth || false,
        onBind: () => {
          if (status?.github_client_id) {
            handleGitHubOAuth(status.github_client_id)
          }
        },
      },
      {
        id: 'oidc',
        label: t('OIDC'),
        icon: Shield,
        value: (profile as unknown as Record<string, unknown>).oidc_id as
          | string
          | undefined,
        isBound: Boolean(
          (profile as unknown as Record<string, unknown>).oidc_id
        ),
        isEnabled: status?.oidc_enabled || false,
        onBind: () => {
          if (status?.oidc_authorization_endpoint && status?.oidc_client_id) {
            handleOIDCOAuth(
              status.oidc_authorization_endpoint,
              status.oidc_client_id
            )
          }
        },
      },
      {
        id: 'twitter',
        label: t('Twitter'),
        icon: SiX as React.ComponentType<{ className?: string }>,
        value: twitterBinding?.username || twitterBinding?.display_name || '',
        isBound: Boolean(twitterBinding?.bound),
        isEnabled: Boolean(twitterBinding?.enabled),
        onBind: handleStartTwitterBind,
        onUnbind: handleUnbindTwitter,
        busy: pendingAction === 'twitter',
      },
      {
        id: 'linuxdo',
        label: t('LinuxDO'),
        icon: SiLinux as React.ComponentType<{ className?: string }>,
        value: (profile as unknown as Record<string, unknown>).linux_do_id as
          | string
          | undefined,
        isBound: Boolean(
          (profile as unknown as Record<string, unknown>).linux_do_id
        ),
        isEnabled: status?.linuxdo_oauth || false,
        onBind: () => {
          if (status?.linuxdo_client_id) {
            handleLinuxDOOAuth(status.linuxdo_client_id)
          }
        },
      },
    ].filter((binding) => binding.isEnabled)
  }, [
    dialogs,
    handleStartTwitterBind,
    handleUnbindTwitter,
    pendingAction,
    profile,
    status,
    t,
    twitterBinding,
  ])

  if (!profile || !status || loading) return null

  const profileFields = profile as unknown as Record<string, unknown>
  const discordId = profileFields.discord_id as string | undefined
  const telegramId = profileFields.telegram_id as string | undefined
  const showDiscordBinding = Boolean(
    status?.discord_oauth ||
    status?.discord_client_id ||
    status?.discord_group_enabled ||
    discordId
  )
  const discordBound = discordGroupStatus?.bound ?? Boolean(discordId)
  const discordJoined = Boolean(discordGroupStatus?.joined)
  const telegramGroupEnabled = Boolean(status?.telegram_group_enabled)
  const telegramBound = Boolean(telegramGroupStatus?.identified || telegramId)
  const showTelegramBinding = telegramGroupEnabled || telegramBound

  let telegramDescription = t(
    'Identify with the Bot, then join the Telegram community'
  )
  if (telegramGroupStatus?.status === 'waiting_for_bot') {
    telegramDescription = t(
      'Open Telegram and press Start; this page will update automatically'
    )
  }
  if (telegramGroupStatus?.identified) {
    telegramDescription = t(
      'Join the community; this page will update automatically'
    )
  }
  if (telegramGroupStatus?.joined) {
    telegramDescription = t('Membership verified')
  }

  let telegramActionLabel = t('Join Telegram community')
  if (telegramGroupStatus?.status === 'waiting_for_bot') {
    telegramActionLabel = t('Open Telegram')
  }
  if (telegramGroupStatus?.status === 'expired') {
    telegramActionLabel = t('Try again')
  }
  if (telegramGroupStatus?.identified) {
    telegramActionLabel = t('Join community')
  }
  if (telegramGroupStatus?.joined) {
    telegramActionLabel = t('Joined')
  }
  if (telegramStarting) {
    telegramActionLabel = t('Loading...')
  }

  let discordDescription = t(
    'Authorize Discord and automatically continue to the community invite'
  )
  if (!discordGroupStatus?.configured) {
    discordDescription = t(
      'Discord community verification is temporarily unavailable'
    )
  } else if (discordBound) {
    discordDescription = t(
      'Join the Discord community; this page will update automatically'
    )
  }
  if (discordJoined) {
    discordDescription = t('Membership verified')
  }

  let discordActionLabel = t('Bind and join Discord')
  if (discordBound) {
    discordActionLabel = t('Join Discord')
  }
  if (discordChecking) {
    discordActionLabel = t('Checking...')
  }
  if (discordJoined) {
    discordActionLabel = t('Joined')
  }
  if (discordBound && discordGroupStatus?.status === 'service_unavailable') {
    discordActionLabel = discordGroupStatus.configured
      ? t('Try again')
      : t('Unavailable')
  }

  let handleDiscordAction = handleStartDiscordBind
  if (discordBound) {
    handleDiscordAction = handleDiscordCommunity
  }
  if (
    discordBound &&
    discordGroupStatus?.configured &&
    discordGroupStatus.status === 'service_unavailable'
  ) {
    handleDiscordAction = async () => {
      await fetchDiscordGroupStatus()
    }
  }

  return (
    <>
      <div className='grid grid-cols-1 gap-2.5 sm:grid-cols-2 sm:gap-3'>
        {bindings.map((binding) => (
          <div
            key={binding.id}
            className='flex items-center justify-between gap-2.5 rounded-lg border p-2.5 sm:gap-3 sm:p-3'
          >
            <div className='flex min-w-0 items-center gap-2.5 sm:gap-3'>
              <div className='bg-muted shrink-0 rounded-md p-1.5 sm:p-2'>
                <binding.icon className='h-4 w-4' />
              </div>
              <div className='min-w-0'>
                <div className='flex items-center gap-1.5'>
                  <p className='text-sm font-medium'>{binding.label}</p>
                  {binding.isBound && (
                    <StatusBadge
                      label={t('Bound')}
                      variant='success'
                      copyable={false}
                    />
                  )}
                </div>
                <p className='text-muted-foreground truncate text-xs'>
                  {binding.value || t('Not bound')}
                </p>
              </div>
            </div>
            {binding.onUnbind && binding.isBound ? (
              <Button
                variant='ghost'
                size='sm'
                className='text-destructive hover:text-destructive h-7 shrink-0 px-2.5 text-xs'
                onClick={binding.onUnbind}
                disabled={binding.busy}
              >
                {binding.busy ? t('Loading...') : t('Unbind')}
              </Button>
            ) : (
              <Button
                variant='outline'
                size='sm'
                className='h-7 shrink-0 px-2.5 text-xs'
                onClick={binding.onBind}
                disabled={
                  binding.busy ||
                  (binding.isBound &&
                    binding.id !== 'email' &&
                    typeof binding.onUnbind !== 'function')
                }
              >
                {binding.busy
                  ? t('Loading...')
                  : binding.isBound
                    ? binding.id === 'email'
                      ? t('Change')
                      : t('Bound')
                    : t('Bind')}
              </Button>
            )}
          </div>
        ))}
      </div>

      {(showTelegramBinding || showDiscordBinding) && (
        <div className='mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2'>
          {showTelegramBinding && (
            <div className='rounded-lg border p-3'>
              <div className='flex h-full flex-col gap-3 sm:flex-row sm:items-center sm:justify-between'>
                <div className='flex min-w-0 items-center gap-2.5 sm:gap-3'>
                  <div className='bg-muted shrink-0 rounded-md p-1.5 sm:p-2'>
                    <Send className='h-4 w-4' />
                  </div>
                  <div className='min-w-0'>
                    <div className='flex items-center gap-1.5'>
                      <p className='text-sm font-medium'>
                        {t('Telegram Community')}
                      </p>
                      {telegramGroupStatus?.joined && (
                        <StatusBadge
                          label={t('Joined')}
                          variant='success'
                          copyable={false}
                        />
                      )}
                    </div>
                    <p className='text-muted-foreground text-xs'>
                      {telegramDescription}
                    </p>
                  </div>
                </div>
                <div className='flex shrink-0 flex-wrap gap-2'>
                  {telegramGroupEnabled && (
                    <Button
                      variant={
                        telegramGroupStatus?.joined ? 'outline' : 'default'
                      }
                      size='sm'
                      className='h-7 px-2.5 text-xs'
                      onClick={handleTelegramCommunity}
                      disabled={
                        telegramStarting || telegramGroupStatus?.joined
                      }
                    >
                      {telegramActionLabel}
                    </Button>
                  )}
                  {telegramBound && (
                    <Button
                      variant='ghost'
                      size='sm'
                      className='text-destructive hover:text-destructive h-7 px-2.5 text-xs'
                      onClick={() => setTelegramUnbindOpen(true)}
                      disabled={telegramStarting || telegramUnbinding}
                    >
                      <Unlink className='mr-1 h-3 w-3' />
                      {telegramUnbinding ? t('Loading...') : t('Unbind')}
                    </Button>
                  )}
                </div>
              </div>
            </div>
          )}

          {showDiscordBinding && (
            <div className='rounded-lg border p-3'>
              <div className='flex h-full flex-col gap-3 sm:flex-row sm:items-center sm:justify-between'>
                <div className='flex min-w-0 items-center gap-2.5 sm:gap-3'>
                  <div className='bg-muted shrink-0 rounded-md p-1.5 sm:p-2'>
                    <IconDiscord className='h-4 w-4' />
                  </div>
                  <div className='min-w-0'>
                    <div className='flex items-center gap-1.5'>
                      <p className='text-sm font-medium'>{t('Discord')}</p>
                      {(discordBound || discordJoined) && (
                        <StatusBadge
                          label={
                            discordJoined ? t('Joined') : t('Pending join')
                          }
                          variant={discordJoined ? 'success' : 'warning'}
                          copyable={false}
                        />
                      )}
                    </div>
                    <p className='text-muted-foreground text-xs'>
                      {discordDescription}
                    </p>
                    {discordBound && discordId ? (
                      <p className='text-muted-foreground mt-0.5 truncate text-[11px]'>
                        {discordId}
                      </p>
                    ) : null}
                  </div>
                </div>
                <div className='flex shrink-0 flex-wrap gap-2'>
                  <Button
                    variant={discordJoined ? 'outline' : 'default'}
                    size='sm'
                    className='h-7 px-2.5 text-xs'
                    onClick={handleDiscordAction}
                    disabled={
                      pendingAction === 'discord' ||
                      discordChecking ||
                      discordJoined ||
                      (discordBound &&
                        discordGroupStatus?.status === 'service_unavailable' &&
                        !discordGroupStatus.configured) ||
                      (!discordBound && !status?.discord_client_id)
                    }
                  >
                    {discordActionLabel}
                  </Button>
                </div>
              </div>
            </div>
          )}
        </div>
      )}

      {/* Custom OAuth Bindings */}
      {customProviders && customProviders.length > 0 && (
        <>
          <Separator className='my-4' />
          <p className='text-muted-foreground mb-3 text-sm font-medium'>
            {t('Custom OAuth')}
          </p>
          <div className='grid grid-cols-1 gap-2.5 sm:grid-cols-2 sm:gap-3'>
            {customProviders.map((provider) => {
              const binding = customBindings.find(
                (b) => b.provider_id === provider.id
              )
              const isBound = !!binding
              return (
                <div
                  key={provider.id}
                  className='flex items-center justify-between gap-2.5 rounded-lg border p-2.5 sm:gap-3 sm:p-3'
                >
                  <div className='flex min-w-0 items-center gap-2.5 sm:gap-3'>
                    <div className='bg-muted shrink-0 rounded-md p-1.5 sm:p-2'>
                      <Link2 className='h-4 w-4' />
                    </div>
                    <div className='min-w-0'>
                      <div className='flex items-center gap-1.5'>
                        <p className='text-sm font-medium'>{provider.name}</p>
                        {isBound && (
                          <StatusBadge
                            label={t('Bound')}
                            variant='success'
                            copyable={false}
                          />
                        )}
                      </div>
                      <p className='text-muted-foreground truncate text-xs'>
                        {isBound
                          ? binding?.external_id || t('Bound')
                          : t('Not bound')}
                      </p>
                    </div>
                  </div>
                  {isBound ? (
                    <Button
                      variant='ghost'
                      size='sm'
                      className='text-destructive hover:text-destructive h-7 shrink-0 px-2.5 text-xs'
                      onClick={() => setUnbindTarget(binding)}
                    >
                      <Unlink className='mr-1 h-3 w-3' />
                      {t('Unbind')}
                    </Button>
                  ) : (
                    <Button
                      variant='outline'
                      size='sm'
                      className='h-7 shrink-0 px-2.5 text-xs'
                      onClick={() => handleBindCustomOAuth(provider)}
                    >
                      {t('Bind')}
                    </Button>
                  )}
                </div>
              )
            })}
          </div>
        </>
      )}

      {/* Custom OAuth Unbind Confirmation */}
      <ConfirmDialog
        open={telegramUnbindOpen}
        onOpenChange={setTelegramUnbindOpen}
        title={t('Confirm Unbind')}
        desc={t(
          'Are you sure you want to unbind {{provider}}? You will no longer be able to log in via this method.',
          { provider: 'Telegram' }
        )}
        confirmText={t('Confirm Unbind')}
        destructive
        handleConfirm={handleUnbindTelegram}
        isLoading={telegramUnbinding}
      />

      <ConfirmDialog
        open={!!unbindTarget}
        onOpenChange={(open) => !open && setUnbindTarget(null)}
        title={t('Confirm Unbind')}
        desc={t(
          'Are you sure you want to unbind {{provider}}? You will no longer be able to log in via this method.',
          {
            provider: unbindTarget?.provider_name || '',
          }
        )}
        confirmText={t('Confirm Unbind')}
        destructive
        handleConfirm={handleUnbindCustom}
        isLoading={unbinding}
      />

      {/* Email Bind Dialog */}
      <EmailBindDialog
        open={dialogs.isOpen('email')}
        onOpenChange={(open) =>
          open ? dialogs.open('email') : dialogs.close('email')
        }
        currentEmail={profile.email}
        onSuccess={onUpdate}
      />

      {/* WeChat Bind Dialog */}
      <WeChatBindDialog
        open={dialogs.isOpen('wechat')}
        onOpenChange={(open) =>
          open ? dialogs.open('wechat') : dialogs.close('wechat')
        }
        onSuccess={onUpdate}
      />
    </>
  )
}
