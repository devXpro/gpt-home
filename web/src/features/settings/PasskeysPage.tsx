import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Fingerprint, KeyRound, Plus, ScanFace } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Page } from '@/components/page'
import { Button } from '@/components/ui/button'
import { TextField } from '@/components/ui/controls'
import { ErrorState, SkeletonRows } from '@/components/ui/feedback'
import { Row, Section } from '@/components/ui/list'
import { ActionSheet, Sheet, SheetFooter } from '@/components/ui/sheet'
import { IconTile } from '@/components/ui/tile'
import { useStepUp } from '@/features/auth/stepup'
import { currentLang } from '@/i18n'
import { api, isApiError } from '@/lib/api'
import { errorText } from '@/lib/errors'
import { fmtDate, fmtRelative } from '@/lib/format'
import { useDelayed, useNow, useSticky } from '@/lib/hooks'
import { ceremonyError, createCredential, type CreationOptions } from '@/lib/passkeys'
import { passkeyName } from '@/lib/platform'
import { qk, usePasskeys } from '@/lib/queries'
import { toast } from '@/lib/toast'
import type { Ceremony, List, Passkey } from '@/lib/types'

// Ключи входа (DESIGN.md §8.9): rename, remove (never the last one), add one more — the dangerous parts behind
// step-up.

function glyphFor(name: string) {
  if (/iphone|ipad/i.test(name)) return ScanFace
  if (/mac/i.test(name)) return Fingerprint
  return KeyRound
}

export function PasskeysPage() {
  const { t } = useTranslation()
  const lang = currentLang()
  const qc = useQueryClient()
  const { withStepUp } = useStepUp()
  const passkeys = usePasskeys()
  const now = useNow()
  const showSkeleton = useDelayed(passkeys.isPending)
  const [open, setOpen] = useState<Passkey | null>(null)
  const [adding, setAdding] = useState(false)
  const items = passkeys.data?.items ?? []

  const add = async () => {
    setAdding(true)
    try {
      const begin = await withStepUp(() => api.post<Ceremony<CreationOptions>>('/me/passkeys/begin', {}), t('stepup.action.passkey_add'))
      if (!begin) return
      const credential = await createCredential(begin.publicKey)
      // A slow ceremony can outlive the step-up window: finish then asks again and is retried (T2.2).
      const pk = await withStepUp(
        () => api.post<Passkey>('/me/passkeys/finish', { ceremonyId: begin.ceremonyId, credential, name: passkeyName() }),
        t('stepup.action.passkey_add'),
      )
      if (!pk) return
      qc.setQueryData<List<Passkey>>(qk.passkeys, (l) => l && { items: [...l.items, pk] })
      toast(t('settings.passkeys.added'))
    } catch (err) {
      if (isApiError(err) || ceremonyError(err) !== 'cancelled') {
        toast(isApiError(err) ? errorText(err, t) : ceremonyError(err) === 'exists' ? t('error.passkey_exists') : t('stepup.failed'), 'warning')
      }
    } finally {
      setAdding(false)
    }
  }

  return (
    <Page title={t('settings.passkeys')} back={{ to: '/admin/settings', label: t('settings.title') }}>
      {passkeys.error && !passkeys.data ? (
        <ErrorState text={errorText(passkeys.error, t)} onRetry={() => void passkeys.refetch()} />
      ) : !passkeys.data ? (
        showSkeleton && <SkeletonRows rows={2} />
      ) : (
        <>
          <Section footer={t('settings.passkeys.footer')}>
            {items.map((pk) => (
              <Row
                key={pk.id}
                onClick={() => setOpen(pk)}
                iconInset
                leading={<IconTile icon={glyphFor(pk.name)} color="gray" />}
                title={
                  <span className="inline-flex items-center gap-2">
                    {pk.name}
                    {pk.current && (
                      <span className="rounded-xs bg-accent-tint px-1.5 py-px text-[12px] leading-4 font-semibold text-accent-tinted">
                        {t('settings.passkeys.current')}
                      </span>
                    )}
                  </span>
                }
                subtitle={
                  pk.lastUsedAt
                    ? t('settings.passkeys.meta', { date: fmtDate(pk.createdAt, lang), time: fmtRelative(pk.lastUsedAt, lang, now) })
                    : t('settings.passkeys.meta_new', { date: fmtDate(pk.createdAt, lang) })
                }
                chevron
              />
            ))}
          </Section>
          <Button className="mt-6 w-full" variant="tinted" size="md" loading={adding} onClick={() => void add()} icon={<Plus aria-hidden className="size-5" />}>
            {t('settings.passkeys.add')}
          </Button>
        </>
      )}
      <PasskeySheet passkey={open} only={items.length <= 1} onClose={() => setOpen(null)} />
    </Page>
  )
}

function PasskeySheet({ passkey, only, onClose }: { passkey: Passkey | null; only: boolean; onClose: () => void }) {
  const shown = useSticky(passkey)
  return (
    <Sheet open={!!passkey} onClose={onClose} title={shown?.name ?? ''}>
      {shown && <PasskeyForm key={shown.id} passkey={shown} only={only} onDone={onClose} />}
    </Sheet>
  )
}

function PasskeyForm({ passkey, only, onDone }: { passkey: Passkey; only: boolean; onDone: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const { withStepUp } = useStepUp()
  const [name, setName] = useState(passkey.name)
  const [confirm, setConfirm] = useState(false)
  const rename = useMutation({
    mutationFn: () => api.patch<Passkey>(`/me/passkeys/${encodeURIComponent(passkey.id)}`, { name: name.trim() }),
    onSuccess: (pk) => {
      qc.setQueryData<List<Passkey>>(qk.passkeys, (l) => l && { items: l.items.map((x) => (x.id === pk.id ? pk : x)) })
      toast(t('common.saved'))
      onDone()
    },
    onError: (err) => toast(errorText(err, t), 'warning'),
  })
  const remove = async () => {
    setConfirm(false)
    try {
      const done = await withStepUp(async () => {
        await api.del(`/me/passkeys/${encodeURIComponent(passkey.id)}`)
        return true
      }, t('stepup.action.passkey_remove'))
      if (!done) return
      qc.setQueryData<List<Passkey>>(qk.passkeys, (l) => l && { items: l.items.filter((x) => x.id !== passkey.id) })
      onDone()
    } catch (err) {
      toast(errorText(err, t), 'warning')
    }
  }
  return (
    <form
      id="passkey-form"
      onSubmit={(e) => {
        e.preventDefault()
        if (name.trim()) rename.mutate()
      }}
    >
      <TextField label={t('settings.passkeys.name')} value={name} onChange={(e) => setName(e.target.value)} maxLength={40} autoComplete="off" />
      <div className="mt-6">
        <Button variant="destructive" size="md" className="w-full" disabled={only} onClick={() => setConfirm(true)}>
          {t('common.delete')}
        </Button>
        {only && <p className="mt-2 px-1 type-footnote text-label-2">{t('settings.passkeys.last')}</p>}
      </div>
      <SheetFooter>
        <Button type="submit" form="passkey-form" variant="filled" size="lg" loading={rename.isPending} disabled={!name.trim() || name.trim() === passkey.name}>
          {t('common.save')}
        </Button>
      </SheetFooter>
      <ActionSheet
        open={confirm}
        title={t('settings.passkeys.remove.confirm', { name: passkey.name })}
        action={t('common.delete')}
        onConfirm={() => void remove()}
        onCancel={() => setConfirm(false)}
      />
    </form>
  )
}
