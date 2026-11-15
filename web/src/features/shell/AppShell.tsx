import { useQueryClient } from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import { CloudOff } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'
import { Navigate, ScrollRestoration, useLocation, useMatches, useNavigate, useNavigationType, useOutlet } from 'react-router'
import { Button } from '@/components/ui/button'
import { EmptyState, ErrorState } from '@/components/ui/feedback'
import { Spinner } from '@/components/ui/spinner'
import { setLanguage } from '@/i18n'
import { api, connectivity, isApiError, isOffline, onUnauthenticated } from '@/lib/api'
import { sessionRevokedBus, useAdminEvents } from '@/lib/events'
import { fmtClock } from '@/lib/format'
import { useNow } from '@/lib/hooks'
import { fade, spring, type Direction } from '@/lib/motion'
import { useOverview, useRunningCalls, useRunningJobs, useSession } from '@/lib/queries'
import { toast } from '@/lib/toast'
import { moduleTitle, toolTitle } from '@/lib/tools'
import type { Call, Job, PluginDetail, PluginSummary } from '@/lib/types'
import { StepUpProvider } from '@/features/auth/stepup'
import { JobProvider, useJobs } from '@/features/run/jobs'
import { RunProvider, useRun } from '@/features/run/run'
import { currentLang } from '@/i18n'
import { DirectionContext } from './direction'
import { recordNavigation, rememberTabPath, type TabId } from './navstack'
import { Sidebar, TabBar } from './tabs'

/** PageHandle is the `handle` of every admin route: which page it shows, in which tab, how deep. */
export interface PageHandle {
  page: string
  tab?: TabId
  depth: number
}

function usePageInfo() {
  const matches = useMatches()
  const leaf = matches[matches.length - 1]
  const handle = leaf?.handle as PageHandle | undefined
  const key = `${handle?.page ?? leaf?.pathname ?? ''}:${leaf?.params.pluginId ?? ''}`
  return { key, handle }
}

/** AppShell is the signed-in frame: tab bar or sidebar, banners, the running pill, and the page transitions. */
export function AppShell() {
  const { t } = useTranslation()
  const session = useSession()
  const location = useLocation()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const hadSession = useRef(false)
  useEffect(() => {
    if (session.data) hadSession.current = true
  }, [session.data])

  const me = session.data
  const lang = me?.person.language ?? me?.home.language ?? 'ru'
  useEffect(() => {
    if (me) setLanguage(lang)
  }, [me, lang])

  useAdminEvents(qc, !!me)

  // 401 anywhere: back to sign-in, with a toast when a session was there before (DESIGN.md §7).
  useEffect(() => {
    const goLogin = () => {
      const next = window.location.pathname + window.location.search
      if (hadSession.current) toast(t('session.expired'), 'info')
      hadSession.current = false
      qc.clear()
      navigate(`/admin/login?next=${encodeURIComponent(next)}`, { replace: true })
    }
    const off1 = onUnauthenticated(goLogin)
    const off2 = sessionRevokedBus.on(goLogin)
    return () => {
      off1()
      off2()
    }
  }, [navigate, qc, t])

  // Back online: refetch what is on screen.
  useEffect(() => connectivity.onBack(() => void qc.invalidateQueries()), [qc])

  if (session.isPending) return <Splash />
  if (session.error) {
    if (isApiError(session.error, 'unauthenticated')) {
      const next = location.pathname + location.search
      return <Navigate to={`/admin/login?next=${encodeURIComponent(next)}`} replace />
    }
    if (isOffline(session.error)) return <OfflineScreen onRetry={() => void session.refetch()} />
    return (
      <main className="flex min-h-dvh items-center justify-center">
        <ErrorState onRetry={() => void session.refetch()} />
      </main>
    )
  }
  return (
    <StepUpProvider>
      <JobProvider>
        <RunProvider>
          <Frame homeName={me?.home.name ?? ''} personName={me?.person.name ?? ''} />
        </RunProvider>
      </JobProvider>
    </StepUpProvider>
  )
}

function Splash() {
  return <div aria-busy="true" className="min-h-dvh bg-canvas" />
}

export function OfflineScreen({ onRetry }: { onRetry: () => void }) {
  const { t } = useTranslation()
  return (
    <main className="flex min-h-dvh items-center justify-center px-6">
      <EmptyState
        icon={CloudOff}
        iconClassName="text-warning"
        title={t('offline.title')}
        text={t('offline.text')}
        action={
          <Button variant="tinted" size="lg" onClick={onRetry}>
            {t('offline.retry')}
          </Button>
        }
      />
    </main>
  )
}

function Frame({ homeName, personName }: { homeName: string; personName: string }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const location = useLocation()
  const navType = useNavigationType()
  const { handle } = usePageInfo()
  const offline = useSyncExternalStore(connectivity.subscribe, connectivity.getSnapshot, () => false)
  const overview = useOverview()
  const tab = handle?.tab

  useEffect(() => {
    recordNavigation(navType, location.key, location.pathname)
    if (tab) rememberTabPath(tab, location.pathname)
  }, [navType, location.key, location.pathname, tab])

  const badges: Partial<Record<TabId, boolean>> = {
    home: (overview.data?.attention.length ?? 0) > 0,
    devices: overview.data?.plugins.some((p) => p.state === 'failing') ?? false,
  }

  const logout = async () => {
    try {
      await api.post('/auth/logout', {})
    } catch {
      // signed out locally anyway
    }
    qc.clear()
    navigate('/admin/login', { replace: true })
  }

  return (
    <div className="min-h-dvh lg:pl-[var(--sidebar-w)]">
      <a href="#main" className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-50 focus:rounded-md focus:bg-dialog focus:px-3 focus:py-2">
        {t('common.open')}: {t('app.name')}
      </a>
      <Sidebar current={tab} badges={badges} homeName={homeName} personName={personName} onLogout={() => void logout()} pill={<RunningPill inline />} />
      <OfflineBanner offline={offline} />
      <div inert={offline} className={offline ? 'opacity-60 transition-opacity duration-150' : 'transition-opacity duration-150'}>
        <AnimatedOutlet />
      </div>
      <TabBar current={tab} badges={badges} pill={<RunningPill />} />
      <ScrollRestoration
        getKey={(loc, matches) => {
          const h = matches[matches.length - 1]?.handle as PageHandle | undefined
          return h ? `${h.page}:${matches[matches.length - 1]?.params.pluginId ?? ''}` : loc.pathname
        }}
      />
    </div>
  )
}



/**
 * AnimatedOutlet animates page changes (DESIGN.md §4): push and pop slide 24 px and fade, tab switches only
 * cross-fade. The wrapper animates opacity only, so a page's fixed nav bar keeps the viewport as its containing
 * block; the page's <main> carries the slide. Pages share one grid cell while they cross, so nothing shifts.
 */
function AnimatedOutlet() {
  const outlet = useOutlet()
  const { key, handle } = usePageInfo()
  const [prev, setPrev] = useState({ key, tab: handle?.tab, depth: handle?.depth ?? 0, dir: 'tab' as Direction })
  let dir = prev.dir
  if (prev.key !== key) {
    const depth = handle?.depth ?? 0
    dir = handle?.tab && handle.tab === prev.tab ? (depth > prev.depth ? 'push' : depth < prev.depth ? 'pop' : 'tab') : 'tab'
    setPrev({ key, tab: handle?.tab, depth, dir })
  }
  return (
    <DirectionContext.Provider value={dir}>
    <div className="grid">
      <AnimatePresence initial={false} custom={dir}>
        <motion.div
          key={key}
          custom={dir}
          className="col-start-1 row-start-1 min-w-0"
          variants={{
            enter: { opacity: 0 },
            center: { opacity: 1, transition: spring.smooth },
            exit: { opacity: 0, transition: fade.fast },
          }}
          initial="enter"
          animate="center"
          exit="exit"
        >
          {outlet}
        </motion.div>
      </AnimatePresence>
    </div>
    </DirectionContext.Provider>
  )
}

function OfflineBanner({ offline }: { offline: boolean }) {
  const { t } = useTranslation()
  return (
    <AnimatePresence>
      {offline && (
        <motion.div
          role="status"
          initial={{ y: -16, opacity: 0 }}
          animate={{ y: 0, opacity: 1 }}
          exit={{ y: -16, opacity: 0 }}
          transition={fade.fast}
          className="fixed inset-x-0 top-[calc(44px+env(safe-area-inset-top))] z-20 gutter-x pt-2 lg:left-[var(--sidebar-w)]"
        >
          <div className="mx-auto flex max-w-[720px] items-start gap-3 rounded-lg bg-surface-2 p-3.5 shadow-[var(--shadow-toast)] ring-[0.5px] ring-separator">
            <CloudOff aria-hidden className="mt-0.5 size-5 shrink-0 text-warning" />
            <div className="min-w-0 flex-1">
              <p className="type-headline">{t('offline.title')}</p>
              <p className="type-subheadline text-label-2">{t('offline.text')}</p>
            </div>
            <Button variant="plain" size="sm" onClick={connectivity.retryNow}>
              {t('offline.retry')}
            </Button>
          </div>
        </motion.div>
      )}
    </AnimatePresence>
  )
}

function jobTitle(job: Job, plugins: PluginSummary[] | undefined, t: TFunction) {
  const p = plugins?.find((x) => x.id === job.target)
  const title = p ? moduleTitle(p, t) : (job.target ?? '')
  if (job.kind === 'plugin.install') return t('add.installing', { title })
  return t(`job.title.${job.kind}`, { title, defaultValue: title })
}

/** RunningPill floats above the tab bar (or sits in the sidebar) while an action runs with its sheet closed. */
function RunningPill({ inline }: { inline?: boolean }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const { openCall, shownCallId } = useRun()
  const { openJob, shownJobId } = useJobs()
  const calls = (useRunningCalls().data?.items ?? []).filter((c) => c.id !== shownCallId && c.state === 'running')
  const jobs = (useRunningJobs().data?.items ?? []).filter((j) => j.id !== shownJobId && j.state === 'running')
  const overview = useOverview()
  const now = useNow(1000)
  const count = calls.length + jobs.length
  const first = (calls[0] as Call | undefined) ?? (jobs[0] as Job | undefined)
  const lang = currentLang()
  let label = ''
  if (first && 'tool' in first) {
    const tool = qc.getQueryData<PluginDetail>(['plugin', first.pluginId])?.tools.find((x) => x.name === first.tool)
    label = tool ? toolTitle(tool, lang) : first.title
  } else if (first) {
    label = jobTitle(first, overview.data?.plugins, t)
  }
  const open = () => {
    if (!first) return
    if ('tool' in first) openCall(first)
    else openJob({ jobId: first.id, title: jobTitle(first, overview.data?.plugins, t) })
  }
  return (
    <AnimatePresence>
      {first && (
        <motion.button
          type="button"
          onClick={open}
          aria-label={t('run.reopen', { title: count > 1 ? t('run.running_many', { count }) : label })}
          initial={{ opacity: 0, y: 8, scale: 0.98 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          exit={{ opacity: 0, y: 8, scale: 0.98 }}
          transition={spring.snappy}
          whileTap={{ scale: 0.97 }}
          className={
            inline
              ? 'flex h-10 w-full items-center gap-2.5 rounded-md bg-accent-tint px-3 text-left text-accent-tinted'
              : 'pointer-events-auto flex h-11 max-w-full items-center gap-2.5 rounded-full bg-dialog px-4 shadow-[var(--shadow-toast)] ring-[0.5px] ring-separator'
          }
        >
          <Spinner size="sm" className={inline ? 'text-current' : 'text-accent'} />
          <span className="min-w-0 flex-1 truncate text-[15px] leading-5 font-semibold">
            {count > 1 ? t('run.running_many', { count }) : label}
          </span>
          {count === 1 && (
            <span className="text-[13px] text-label-2 tabular-nums">{fmtClock(now - new Date(first.startedAt).getTime())}</span>
          )}
        </motion.button>
      )}
    </AnimatePresence>
  )
}
