import type { TFunction } from 'i18next'
import { useMemo } from 'react'
import { currentLang } from '@/i18n'
import { useAllTools } from '@/lib/queries'
import { humanize, toolTitle } from '@/lib/tools'
import type { AuditItem, Tool } from '@/lib/types'

/** useToolIndex maps tool names to tools, for translated titles of past calls. */
export function useToolIndex() {
  const tools = useAllTools()
  return useMemo(() => new Map((tools.data?.items ?? []).map((t) => [t.name, t] as const)), [tools.data])
}

/** auditTitle is an entry's title: the tool's title in the UI language, or the admin action's words (§10.7). */
export function auditTitle(item: AuditItem, tools: Map<string, Tool>, t: TFunction) {
  if (item.kind === 'admin') return t(`audit.${item.action ?? ''}`, { defaultValue: item.action ?? '' })
  const tool = item.tool ? tools.get(item.tool) : undefined
  if (tool) return toolTitle(tool, currentLang())
  return item.toolTitle ?? humanize(item.tool ?? '', item.pluginId)
}
