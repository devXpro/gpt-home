import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Laptop, Monitor, Smartphone } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { Page } from '@/components/page'
import { Button } from '@/components/ui/button'
import { ErrorState, SkeletonRows } from '@/components/ui/feedback'
import { Row, Section } from '@/components/ui/list'
import { ActionSheet } from '@/components/ui/sheet'
import { IconTile } from '@/components/ui/tile'
import { currentLang } from '@/i18n'
import { api } from '@/lib/api'
import { errorText } from '@/lib/errors'
import { fmtRelative } from '@/lib/format'
import { useDelayed, useNow } from '@/lib/hooks'
import { qk, useSessions } from '@/lib/queries'
import { toast } from '@/lib/toast'
import type { List, Session } from '@/lib/types'

// Где выполнен вход (DESIGN.md §8.9): sign out one device, or all the others.

function glyphFor(device: string) {
  if (/iphone|android|phone/i.test(device)) return Smartphone
  if (/mac|book|laptop/i.test(device)) return Laptop
  return Monitor
}

export function SessionsPage() {
  const { t } = useTranslation()
  const lang = currentLang()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const sessions = useSessions()
  const now = useNow()
  const showSkeleton = useDelayed(sessions.isPending)
  const [target, setTarget] = useState<Session | null>(null)
  const [others, setOthers] = useState(false)
  const items = sessions.data?.items ?? []

  const signOut = useMutation({
    mutationFn: (s: Session) => api.del(`/me/sessions/${encodeURIComponent(s.id)}`),
    onSuccess: (_r, s) => {
      if (s.current) {
        qc.clear()
        navigate('/admin/login', { replace: true })
        return
      }
      qc.setQueryData<List<Session>>(qk.sessions, (l) => l && { items: l.items.filter((x) => x.id !== s.id) })
    },
    onError: (err) => toast(errorText(err, t), 'warning'),
  })
  const signOutOthers = useMutation({
    mutationFn: () => api.post<{ revoked: number }>('/me/sessions/revoke-others', {}),
    onSuccess: (r) => {
      qc.setQueryData<List<Session>>(qk.sessions, (l) => l && { items: l.items.filter((x) => x.current) })
      toast(t('settings.sessions.revoked', { count: r.revoked }))
    },
    onError: (err) => toast(errorText(err, t), 'warning'),
  })

  return (
    <Page title={t('settings.sessions')} back={{ to: '/admin/settings', label: t('settings.title') }}>
      {sessions.error && !sessions.data ? (
        <ErrorState text={errorText(sessions.error, t)} onRetry={() => void sessions.refetch()} />
      ) : !sessions.data ? (
        showSkeleton && <SkeletonRows rows={2} />
      ) : (
        <>
          <Section>
            {items.map((s) => (
              <Row
                key={s.id}
                onClick={() => setTarget(s)}
                iconInset
                leading={<IconTile icon={glyphFor(s.device)} color="teal" />}
                title={
                  <span className="inline-flex items-center gap-2">
                    {s.device}
                    {s.current && (
                      <span className="rounded-xs bg-accent-tint px-1.5 py-px text-[12px] leading-4 font-semibold text-accent-tinted">
                        {t('settings.sessions.this')}
                      </span>
                    )}
                  </span>
                }
                subtitle={t('settings.sessions.active', { time: fmtRelative(s.lastSeenAt, lang, now) })}
                chevron
              />
            ))}
          </Section>
          {items.length > 1 && (
            <Button className="mt-6 w-full" variant="destructive" size="md" loading={signOutOthers.isPending} onClick={() => setOthers(true)}>
              {t('settings.sessions.others')}
            </Button>
          )}
        </>
      )}
      <ActionSheet
        open={!!target}
        title={target?.device ?? ''}
        action={t('settings.sessions.signout')}
        onConfirm={() => {
          if (target) signOut.mutate(target)
          setTarget(null)
        }}
        onCancel={() => setTarget(null)}
      />
      <ActionSheet
        open={others}
        title={t('settings.sessions.others')}
        action={t('settings.sessions.others')}
        onConfirm={() => {
          setOthers(false)
          signOutOthers.mutate()
        }}
        onCancel={() => setOthers(false)}
      />
    </Page>
  )
}
