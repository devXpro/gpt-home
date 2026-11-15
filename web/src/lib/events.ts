// The admin SSE stream (ADMIN-API §4) feeds the TanStack Query cache: every event carries a whole object, so the
// cache simply replaces what it holds (DESIGN.md §7).

import type { QueryClient } from '@tanstack/react-query'
import { useEffect, useSyncExternalStore } from 'react'
import { qk } from './queries'
import type { AuditItem, Call, Job, List, Me, Overview, PluginDetail, PluginSummary, RelayStatus, Settings, Bootstrap } from './types'

type Handler<T> = (value: T) => void

/** A Bus is a tiny event emitter for things the cache alone cannot say (a call ended, an entry arrived). */
export function createBus<T>() {
  const handlers = new Set<Handler<T>>()
  return {
    emit(value: T) {
      for (const h of handlers) h(value)
    },
    on(h: Handler<T>) {
      handlers.add(h)
      return () => {
        handlers.delete(h)
      }
    },
  }
}

export const callBus = createBus<Call>()
export const jobBus = createBus<Job>()
export const auditBus = createBus<AuditItem>()
export const pluginRemovedBus = createBus<string>()
export const sessionRevokedBus = createBus<undefined>()

let connected = false
const connListeners = new Set<() => void>()
function setConnected(v: boolean) {
  if (connected === v) return
  connected = v
  for (const l of connListeners) l()
}

/** useEventsConnected is false while the stream is down; screens then poll what they watch. */
export function useEventsConnected() {
  return useSyncExternalStore(
    (l) => {
      connListeners.add(l)
      return () => {
        connListeners.delete(l)
      }
    },
    () => connected,
    () => false,
  )
}

function replaceIn<T extends { id: string }>(list: T[], item: T, addIfMissing = true) {
  const i = list.findIndex((x) => x.id === item.id)
  if (i === -1) return addIfMissing ? [...list, item] : list
  const next = list.slice()
  next[i] = item
  return next
}

function parse<T>(e: MessageEvent): T | undefined {
  try {
    return JSON.parse(e.data as string) as T
  } catch {
    return undefined
  }
}

/** applyEvent updates the cache for one SSE event; exported for tests. */
export function applyEvent(qc: QueryClient, type: string, data: unknown) {
  switch (type) {
    case 'resync':
      void qc.invalidateQueries()
      break
    case 'relay': {
      const r = data as RelayStatus
      qc.setQueryData(qk.relay, r)
      qc.setQueryData<Overview>(qk.overview, (o) => o && { ...o, relay: r })
      void qc.invalidateQueries({ queryKey: qk.overview, refetchType: 'active' })
      break
    }
    case 'plugin': {
      const p = data as PluginSummary
      qc.setQueryData<List<PluginSummary>>(qk.plugins, (l) => l && { items: replaceIn(l.items, p) })
      qc.setQueryData<Overview>(qk.overview, (o) => o && { ...o, plugins: replaceIn(o.plugins, p) })
      qc.setQueryData<PluginDetail>(qk.plugin(p.id), (d) => {
        if (!d) return d
        const { tools: _counts, ...summary } = p
        return { ...d, ...summary }
      })
      void qc.invalidateQueries({ queryKey: qk.overview, refetchType: 'active' })
      break
    }
    case 'plugin.removed': {
      const { id } = data as { id: string }
      qc.setQueryData<List<PluginSummary>>(qk.plugins, (l) => l && { items: l.items.filter((p) => p.id !== id) })
      qc.setQueryData<Overview>(qk.overview, (o) => o && { ...o, plugins: o.plugins.filter((p) => p.id !== id) })
      pluginRemovedBus.emit(id)
      break
    }
    case 'tools': {
      const { pluginId } = data as { pluginId: string }
      void qc.invalidateQueries({ queryKey: qk.plugin(pluginId) })
      break
    }
    case 'call': {
      const c = data as Call
      qc.setQueryData(qk.call(c.id), c)
      qc.setQueryData<List<Call>>(qk.runningCalls, (l) => {
        const items = l?.items ?? []
        return { items: c.state === 'running' ? replaceIn(items, c) : items.filter((x) => x.id !== c.id) }
      })
      if (c.state === 'ok') void qc.invalidateQueries({ queryKey: qk.inventory(c.pluginId) })
      callBus.emit(c)
      break
    }
    case 'job': {
      const j = data as Job
      qc.setQueryData(qk.job(j.id), j)
      qc.setQueryData<List<Job>>(qk.runningJobs, (l) => {
        const items = l?.items ?? []
        return { items: j.state === 'running' ? replaceIn(items, j) : items.filter((x) => x.id !== j.id) }
      })
      if (j.state !== 'running') {
        void qc.invalidateQueries({ queryKey: qk.plugins })
        void qc.invalidateQueries({ queryKey: qk.overview })
        if (j.target) void qc.invalidateQueries({ queryKey: qk.plugin(j.target) })
        if (j.kind === 'hub.update') void qc.invalidateQueries({ queryKey: qk.system })
      }
      jobBus.emit(j)
      break
    }
    case 'audit': {
      const a = data as AuditItem
      if (a.kind === 'tool' && (a.via === 'site' || a.via === 'mcp')) {
        qc.setQueryData<Overview>(qk.overview, (o) => o && { ...o, recent: [a, ...o.recent.filter((x) => x.id !== a.id)].slice(0, 5) })
      }
      auditBus.emit(a)
      break
    }
    case 'settings': {
      // Owners get the whole Settings, members only {"homeName"}.
      const s = data as Partial<Settings>
      qc.setQueryData<Settings>(qk.settings, (old) => old && { ...old, ...s })
      if (s.homeName) {
        const name = s.homeName
        qc.setQueryData<Me>(qk.session, (m) => m && { ...m, home: { ...m.home, name } })
        qc.setQueryData<Bootstrap>(qk.bootstrap, (b) => b && { ...b, homeName: name })
        qc.setQueryData<Overview>(qk.overview, (o) => o && { ...o, home: { name } })
      }
      break
    }
    case 'session.revoked':
      sessionRevokedBus.emit(undefined)
      break
  }
}

const TYPES = ['hello', 'resync', 'relay', 'plugin', 'plugin.removed', 'tools', 'call', 'job', 'audit', 'settings', 'session.revoked']

/** useAdminEvents keeps one EventSource open while signed in; the browser reconnects with Last-Event-ID. */
export function useAdminEvents(qc: QueryClient, enabled: boolean) {
  useEffect(() => {
    if (!enabled || typeof EventSource === 'undefined') return
    let version: string | undefined
    const es = new EventSource('/api/admin/events')
    es.onopen = () => setConnected(true)
    es.onerror = () => setConnected(false)
    const listeners = TYPES.map((type) => {
      const fn = (e: MessageEvent) => {
        const data = parse<unknown>(e)
        if (type === 'hello') {
          const v = (data as { version?: string } | undefined)?.version
          // A new hub version after a self-update: reload into the new SPA (DESIGN.md §8.9 About).
          if (version && v && v !== version) window.location.reload()
          version = v ?? version
          setConnected(true)
          return
        }
        applyEvent(qc, type, data)
        if (type === 'session.revoked') es.close()
      }
      es.addEventListener(type, fn)
      return [type, fn] as const
    })
    return () => {
      for (const [type, fn] of listeners) es.removeEventListener(type, fn)
      es.close()
      setConnected(false)
    }
  }, [qc, enabled])
}

/** usePluginLogStream follows one plugin's log lines (`?logs=<id>`), appending them to the logs query. */
export function usePluginLogStream(qc: QueryClient, pluginId: string, onLines: (lines: string[]) => void) {
  useEffect(() => {
    if (typeof EventSource === 'undefined') return
    const es = new EventSource(`/api/admin/events?logs=${encodeURIComponent(pluginId)}`)
    const fn = (e: MessageEvent) => {
      const d = parse<{ pluginId: string; lines: string[] }>(e)
      if (!d || d.pluginId !== pluginId) return
      qc.setQueryData<{ lines: string[] }>(qk.logs(pluginId), (old) => ({ lines: [...(old?.lines ?? []), ...d.lines].slice(-5000) }))
      onLines(d.lines)
    }
    es.addEventListener('plugin.log', fn)
    return () => {
      es.removeEventListener('plugin.log', fn)
      es.close()
    }
  }, [qc, pluginId, onLines])
}
