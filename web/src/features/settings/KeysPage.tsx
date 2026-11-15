import { useQueryClient } from '@tanstack/react-query'
import { Key as KeyIcon, Plus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Page } from '@/components/page'
import { Button } from '@/components/ui/button'
import { TextField } from '@/components/ui/controls'
import { SecretOnce } from '@/components/ui/copy'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/ui/feedback'
import { Row, Section } from '@/components/ui/list'
import { ActionSheet, Sheet, SheetFooter } from '@/components/ui/sheet'
import { IconTile } from '@/components/ui/tile'
import { useStepUp } from '@/features/auth/stepup'
import { currentLang } from '@/i18n'
import { api } from '@/lib/api'
import { errorText } from '@/lib/errors'
import { fmtDate, fmtRelative } from '@/lib/format'
import { useDelayed, useNow } from '@/lib/hooks'
import { qk, useKeys, useSession } from '@/lib/queries'
import { toast } from '@/lib/toast'
import type { Key, List, NewKey } from '@/lib/types'

// Ключи доступа (DESIGN.md §8.9, phase 2): the bearer keys ChatGPT Sites use. A new key is shown exactly once.

export function KeysPage() {
  const { t } = useTranslation()
  const lang = currentLang()
  const qc = useQueryClient()
  const { withStepUp } = useStepUp()
  const keys = useKeys()
  const now = useNow()
  const showSkeleton = useDelayed(keys.isPending)
  const [revoking, setRevoking] = useState<Key | null>(null)
  const [creating, setCreating] = useState(false)
  const items = keys.data?.items ?? []

  const revoke = async (k: Key) => {
    setRevoking(null)
    try {
      const done = await withStepUp(async () => {
        await api.del(`/keys/${encodeURIComponent(k.id)}`)
        return true
      }, t('stepup.action.key_revoke'))
      if (!done) return
      qc.setQueryData<List<Key>>(qk.keys, (l) => l && { items: l.items.filter((x) => x.id !== k.id) })
      toast(t('settings.keys.revoked'))
    } catch (err) {
      toast(errorText(err, t), 'warning')
    }
  }

  return (
    <Page title={t('settings.keys')} back={{ to: '/admin/settings', label: t('settings.title') }}>
      {keys.error && !keys.data ? (
        <ErrorState text={errorText(keys.error, t)} onRetry={() => void keys.refetch()} />
      ) : !keys.data ? (
        showSkeleton && <SkeletonRows rows={2} />
      ) : items.length === 0 ? (
        <EmptyState icon={KeyIcon} title={t('settings.keys')} text={t('settings.keys.empty')} />
      ) : (
        <Section footer={t('settings.keys.footer')}>
          {items.map((k) => (
            <Row
              key={k.id}
              onClick={() => setRevoking(k)}
              iconInset
              leading={<IconTile icon={KeyIcon} color="orange" />}
              title={
                <span className="inline-flex flex-wrap items-baseline gap-x-2">
                  {k.label}
                  {k.hint && <code className="font-mono text-[13px] text-label-2">{k.hint}…</code>}
                </span>
              }
              subtitle={
                k.lastUsedAt
                  ? t('settings.keys.meta', { date: fmtDate(k.createdAt, lang), time: fmtRelative(k.lastUsedAt, lang, now) })
                  : t('settings.keys.meta_new', { date: fmtDate(k.createdAt, lang) })
              }
              trailing={<span className="type-body text-danger-text">{t('settings.keys.revoke')}</span>}
            />
          ))}
        </Section>
      )}
      {keys.data && (
        <Button className="mt-6 w-full" variant="tinted" size="md" onClick={() => setCreating(true)} icon={<Plus aria-hidden className="size-5" />}>
          {t('settings.keys.new')}
        </Button>
      )}
      <ActionSheet
        open={!!revoking}
        title={t('settings.keys.revoke.confirm', { label: revoking?.label ?? '' })}
        action={t('settings.keys.revoke')}
        onConfirm={() => revoking && void revoke(revoking)}
        onCancel={() => setRevoking(null)}
      />
      <Sheet open={creating} onClose={() => setCreating(false)} title={t('settings.keys.new')}>
        <NewKeyFlow onDone={() => setCreating(false)} />
      </Sheet>
    </Page>
  )
}

function NewKeyFlow({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const me = useSession().data
  const { withStepUp } = useStepUp()
  const [label, setLabel] = useState(t('settings.keys.label.default'))
  const [secret, setSecret] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  if (secret) return <SecretOnce value={secret} onDone={onDone} />

  const create = async () => {
    if (!me) return
    setBusy(true)
    setError(undefined)
    try {
      const res = await withStepUp(
        () => api.post<NewKey>(`/people/${encodeURIComponent(me.person.id)}/keys`, { label: label.trim() || t('settings.keys.label.default') }),
        t('stepup.action.key_create'),
      )
      if (!res) return
      qc.setQueryData<List<Key>>(qk.keys, (l) => l && { items: [...l.items, res.key] })
      setSecret(res.secret)
    } catch (err) {
      setError(errorText(err, t))
    } finally {
      setBusy(false)
    }
  }
  return (
    <form
      id="key-form"
      onSubmit={(e) => {
        e.preventDefault()
        void create()
      }}
    >
      <TextField label={t('settings.keys.label')} value={label} onChange={(e) => setLabel(e.target.value)} error={error} maxLength={60} autoComplete="off" autoFocusOnOpen />
      <SheetFooter>
        <Button type="submit" form="key-form" variant="filled" size="lg" loading={busy}>
          {t('settings.keys.new')}
        </Button>
      </SheetFooter>
    </form>
  )
}
