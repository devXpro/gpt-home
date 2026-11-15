// TanStack Query hooks for every GET of the admin API (DESIGN.md §7): staleTime 10 s, refetch on focus, previous
// data kept while refetching. The SSE stream (events.ts) keeps these caches current.

import { keepPreviousData, QueryClient, useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { api, ApiError, OfflineError } from './api'
import type {
  AuditItem,
  Bootstrap,
  Call,
  Inventory,
  Job,
  Key,
  List,
  Me,
  Overview,
  Page,
  Passkey,
  Person,
  PluginDetail,
  PluginPreview,
  PluginSummary,
  RelayStatus,
  Session,
  Settings,
  SystemInfo,
  Tool,
} from './types'

export function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 10_000,
        refetchOnWindowFocus: true,
        retry: (count, err) => !(err instanceof ApiError) && !(err instanceof OfflineError) && count < 1,
      },
      mutations: { retry: false },
    },
  })
}

export const qk = {
  bootstrap: ['bootstrap'] as const,
  session: ['session'] as const,
  overview: ['overview'] as const,
  plugins: ['plugins'] as const,
  plugin: (id: string) => ['plugin', id] as const,
  inventory: (id: string) => ['inventory', id] as const,
  logs: (id: string) => ['logs', id] as const,
  preview: (repo: string) => ['preview', repo] as const,
  runningCalls: ['calls', 'running'] as const,
  call: (id: string) => ['call', id] as const,
  runningJobs: ['jobs', 'running'] as const,
  job: (id: string) => ['job', id] as const,
  audit: (filter: AuditFilter) => ['audit', filter] as const,
  auditAll: ['audit'] as const,
  auditItem: (id: string) => ['auditItem', id] as const,
  settings: ['settings'] as const,
  relay: ['relay'] as const,
  system: ['system'] as const,
  passkeys: ['passkeys'] as const,
  sessions: ['sessions'] as const,
  keys: ['keys'] as const,
  people: ['people'] as const,
}

export function useBootstrap() {
  return useQuery({ queryKey: qk.bootstrap, queryFn: () => api.get<Bootstrap>('/bootstrap'), staleTime: 60_000 })
}

export function useSession(enabled = true) {
  return useQuery({ queryKey: qk.session, queryFn: () => api.get<Me>('/session'), enabled, staleTime: 60_000 })
}

export function useOverview() {
  return useQuery({ queryKey: qk.overview, queryFn: () => api.get<Overview>('/overview') })
}

export function usePlugins() {
  return useQuery({ queryKey: qk.plugins, queryFn: () => api.get<List<PluginSummary>>('/plugins') })
}

export function usePlugin(id: string) {
  return useQuery({ queryKey: qk.plugin(id), queryFn: () => api.get<PluginDetail>(`/plugins/${encodeURIComponent(id)}`) })
}

export function useInventory(id: string, enabled: boolean) {
  return useQuery({
    queryKey: qk.inventory(id),
    queryFn: () => api.get<Inventory>(`/plugins/${encodeURIComponent(id)}/inventory`),
    enabled,
    staleTime: 0,
  })
}

export function usePluginLogs(id: string) {
  return useQuery({
    queryKey: qk.logs(id),
    queryFn: () => api.get<{ lines: string[] }>(`/plugins/${encodeURIComponent(id)}/logs`, { query: { tail: 1000 } }),
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  })
}

export function usePreview(repo: string | undefined) {
  return useQuery({
    queryKey: qk.preview(repo ?? ''),
    queryFn: () => api.get<PluginPreview>('/plugins/preview', { query: { repo, version: 'latest' } }),
    enabled: !!repo,
    staleTime: 5 * 60_000,
  })
}

export function useRunningCalls(enabled = true) {
  return useQuery({
    queryKey: qk.runningCalls,
    queryFn: () => api.get<List<Call>>('/calls', { query: { state: 'running' } }),
    enabled,
    staleTime: Infinity,
  })
}

export function useCall(id: string | undefined) {
  return useQuery({
    queryKey: qk.call(id ?? ''),
    queryFn: () => api.get<Call>(`/calls/${encodeURIComponent(id ?? '')}`),
    enabled: !!id,
    staleTime: Infinity,
  })
}

export function useRunningJobs(enabled = true) {
  return useQuery({
    queryKey: qk.runningJobs,
    queryFn: () => api.get<List<Job>>('/jobs', { query: { state: 'running' } }),
    enabled,
    staleTime: Infinity,
  })
}

export function useJob(id: string | undefined) {
  return useQuery({
    queryKey: qk.job(id ?? ''),
    queryFn: () => api.get<Job>(`/jobs/${encodeURIComponent(id ?? '')}`),
    enabled: !!id,
    staleTime: Infinity,
  })
}

/** AuditFilter mirrors the GET /audit query (ADMIN-API §3.6). */
export interface AuditFilter {
  kind?: 'tool' | 'admin'
  via?: string
  status?: 'ok' | 'failed'
  person?: string
  plugin?: string
}

export function useAudit(filter: AuditFilter) {
  return useInfiniteQuery({
    queryKey: qk.audit(filter),
    queryFn: ({ pageParam }) =>
      api.get<Page<AuditItem>>('/audit', { query: { ...filter, limit: 50, cursor: pageParam } }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor,
    placeholderData: keepPreviousData,
  })
}

export function useAuditItem(id: string) {
  return useQuery({ queryKey: qk.auditItem(id), queryFn: () => api.get<AuditItem>(`/audit/${encodeURIComponent(id)}`) })
}

export function useSettings() {
  return useQuery({ queryKey: qk.settings, queryFn: () => api.get<Settings>('/settings') })
}

export function useRelay() {
  return useQuery({ queryKey: qk.relay, queryFn: () => api.get<RelayStatus>('/relay') })
}

export function useSystem() {
  return useQuery({ queryKey: qk.system, queryFn: () => api.get<SystemInfo>('/system') })
}

export function usePasskeys() {
  return useQuery({ queryKey: qk.passkeys, queryFn: () => api.get<List<Passkey>>('/me/passkeys') })
}

export function useSessions() {
  return useQuery({ queryKey: qk.sessions, queryFn: () => api.get<List<Session>>('/me/sessions') })
}

export function useKeys() {
  return useQuery({ queryKey: qk.keys, queryFn: () => api.get<List<Key>>('/keys') })
}

export function usePeople() {
  return useQuery({ queryKey: qk.people, queryFn: () => api.get<List<Person>>('/people') })
}

/** useAllTools lists every tool once, for the translated titles in the activity log. */
export function useAllTools() {
  return useQuery({
    queryKey: ['tools', 'admin'] as const,
    queryFn: () => api.get<List<Tool>>('/tools', { query: { audience: 'admin' } }),
    staleTime: 5 * 60_000,
  })
}
