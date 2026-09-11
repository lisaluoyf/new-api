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
import { getCookie } from '@/lib/cookies'
import { cn } from '@/lib/utils'
import { LayoutProvider } from '@/context/layout-provider'
import { SearchProvider } from '@/context/search-provider'
import { useNotifications } from '@/hooks/use-notifications'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import { NotificationButton } from '@/components/notification-button'
import { NotificationDialog } from '@/components/notification-dialog'
import { AnimatedOutlet } from '@/components/page-transition'
import { SkipToMain } from '@/components/skip-to-main'
import { WorkspaceProvider } from '../context/workspace-context'
import { AppSidebar } from './app-sidebar'
import { MobileNavTabs } from './mobile-nav-tabs'

type AuthenticatedLayoutProps = {
  children?: React.ReactNode
}

export function AuthenticatedLayout(props: AuthenticatedLayoutProps) {
  const defaultOpen = getCookie('sidebar_state') !== 'false'
  const notifications = useNotifications()

  return (
    <LayoutProvider>
      <SearchProvider>
        <WorkspaceProvider>
          <SidebarProvider defaultOpen={defaultOpen} className='flex-col'>
            <SkipToMain />
            <div className='flex min-h-0 w-full flex-1'>
              <AppSidebar />
              <SidebarInset
                className={cn(
                  '@container/content',
                  'h-[calc(100svh-var(--app-header-height,0px))]',
                  'peer-data-[variant=inset]:h-[calc(100svh-var(--app-header-height,0px)-(var(--spacing)*4))]'
                )}
              >
                <div className='border-sidebar-border bg-sidebar flex h-14 shrink-0 items-center border-b'>
                  <div className='min-w-0 flex-1'>
                    <MobileNavTabs />
                  </div>
                  <div className='shrink-0 px-3'>
                    <NotificationButton
                      unreadCount={notifications.unreadCount}
                      onClick={() => notifications.openDialog()}
                    />
                  </div>
                </div>
                {props.children ?? <AnimatedOutlet />}
              </SidebarInset>
            </div>
          </SidebarProvider>
          <NotificationDialog
            open={notifications.dialogOpen}
            onOpenChange={notifications.setDialogOpen}
            activeTab={notifications.activeTab}
            onTabChange={notifications.setActiveTab}
            notice={notifications.notice}
            announcements={notifications.announcements}
            loading={notifications.loading}
            onCloseToday={notifications.closeToday}
          />
        </WorkspaceProvider>
      </SearchProvider>
    </LayoutProvider>
  )
}
