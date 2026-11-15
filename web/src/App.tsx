import { QueryClientProvider } from '@tanstack/react-query'
import { MotionConfig } from 'motion/react'
import { useState } from 'react'
import { createBrowserRouter, Navigate, type RouteObject } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { Toaster } from '@/components/ui/toaster'
import { ActivityPage } from '@/features/activity/ActivityPage'
import { LoginPage } from '@/features/auth/LoginPage'
import { DevicesPage } from '@/features/devices/DevicesPage'
import { LogsPage } from '@/features/devices/LogsPage'
import { ModulePage } from '@/features/devices/ModulePage'
import { HomePage } from '@/features/home/HomePage'
import { NotFoundPage, NotFoundScreen } from '@/features/misc/NotFound'
import { PeoplePage } from '@/features/people/PeoplePage'
import { AboutPage } from '@/features/settings/AboutPage'
import { AppearancePage, LanguagePage } from '@/features/settings/ChoicePages'
import { HomeNamePage } from '@/features/settings/HomeNamePage'
import { KeysPage } from '@/features/settings/KeysPage'
import { PasskeysPage } from '@/features/settings/PasskeysPage'
import { ServerPage } from '@/features/settings/ServerPage'
import { SessionsPage } from '@/features/settings/SessionsPage'
import { SettingsPage } from '@/features/settings/SettingsPage'
import { SetupWizard } from '@/features/setup/SetupWizard'
import { AppShell, type PageHandle } from '@/features/shell/AppShell'
import { makeQueryClient } from '@/lib/queries'


const page = (path: string, Component: React.ComponentType, handle: PageHandle): RouteObject => ({ path, Component, handle })

// Routes of DESIGN.md §2.2. Sheets have their own URL, served by the page under them (so back closes them).
const routes: RouteObject[] = [
  { path: '/', element: <Navigate to="/admin" replace /> },
  { path: '/firstrun', lazy: async () => ({ Component: (await import('@/features/firstrun/LanSetup')).LanSetup }) },
  { path: '/admin/login', Component: LoginPage },
  { path: '/admin/setup', Component: SetupWizard },
  {
    path: '/admin',
    Component: AppShell,
    children: [
      { index: true, Component: HomePage, handle: { page: 'home', tab: 'home', depth: 0 } satisfies PageHandle },
      page('devices', DevicesPage, { page: 'devices', tab: 'devices', depth: 0 }),
      page('devices/add', DevicesPage, { page: 'devices', tab: 'devices', depth: 0 }),
      page('devices/:pluginId', ModulePage, { page: 'module', tab: 'devices', depth: 1 }),
      page('devices/:pluginId/items/:itemId', ModulePage, { page: 'module', tab: 'devices', depth: 1 }),
      page('devices/:pluginId/logs', LogsPage, { page: 'logs', tab: 'devices', depth: 2 }),
      page('people', PeoplePage, { page: 'people', tab: 'people', depth: 0 }),
      page('activity', ActivityPage, { page: 'activity', tab: 'activity', depth: 0 }),
      page('activity/:id', ActivityPage, { page: 'activity', tab: 'activity', depth: 0 }),
      page('settings', SettingsPage, { page: 'settings', tab: 'settings', depth: 0 }),
      page('settings/home', HomeNamePage, { page: 'settings/home', tab: 'settings', depth: 1 }),
      page('settings/language', LanguagePage, { page: 'settings/language', tab: 'settings', depth: 1 }),
      page('settings/appearance', AppearancePage, { page: 'settings/appearance', tab: 'settings', depth: 1 }),
      page('settings/passkeys', PasskeysPage, { page: 'settings/passkeys', tab: 'settings', depth: 1 }),
      page('settings/sessions', SessionsPage, { page: 'settings/sessions', tab: 'settings', depth: 1 }),
      page('settings/server', ServerPage, { page: 'settings/server', tab: 'settings', depth: 1 }),
      page('settings/keys', KeysPage, { page: 'settings/keys', tab: 'settings', depth: 1 }),
      page('settings/about', AboutPage, { page: 'settings/about', tab: 'settings', depth: 1 }),
      page('*', NotFoundPage, { page: 'notfound', depth: 0 }),
    ],
  },
  { path: '*', Component: NotFoundScreen },
]

export function App() {
  const [qc] = useState(makeQueryClient)
  const [router] = useState(() => createBrowserRouter(routes))
  return (
    <QueryClientProvider client={qc}>
      <MotionConfig reducedMotion="user">
        <RouterProvider router={router} />
        <Toaster />
      </MotionConfig>
    </QueryClientProvider>
  )
}
