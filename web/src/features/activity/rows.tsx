import { Shield, XCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Row } from '@/components/ui/list'
import { IconTile, ModuleTile } from '@/components/ui/tile'
import { currentLang } from '@/i18n'
import { fmtDuration, fmtRelative, fmtTime } from '@/lib/format'
import type { AuditItem, Tool } from '@/lib/types'
import { auditTitle } from './toolIndex'

/** ActivityRow is one audit entry: icon, title, "who · how · when", and a red cross or the duration (§8.8). */
export function ActivityRow({
  item,
  tools,
  to,
  relative,
  now,
  flash,
}: {
  item: AuditItem
  tools: Map<string, Tool>
  to?: string
  relative?: boolean
  now: number
  flash?: boolean
}) {
  const { t } = useTranslation()
  const lang = currentLang()
  const time = relative ? fmtRelative(item.at, lang, now) : fmtTime(item.at, lang)
  return (
    <Row
      to={to}
      flash={flash}
      iconInset
      leading={item.kind === 'tool' && item.pluginId ? <ModuleTile id={item.pluginId} size="sm" /> : <IconTile icon={Shield} color="gray" />}
      title={auditTitle(item, tools, t)}
      subtitle={t('activity.row', { person: item.person?.name ?? t('activity.someone'), via: t(`via.${item.via}`), time })}
      trailing={
        !item.ok ? (
          <XCircle role="img" aria-label={t('activity.failed_mark')} className="size-5 text-danger" />
        ) : item.durationMs >= 2000 ? (
          <span className="type-subheadline text-label-2 tabular-nums">{fmtDuration(item.durationMs, lang)}</span>
        ) : undefined
      }
      chevron={!!to}
    />
  )
}
