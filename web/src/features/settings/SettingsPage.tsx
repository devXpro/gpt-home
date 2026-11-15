import { useMutation, useQueryClient } from '@tanstack/react-query'
import { House, Info, Key, KeyRound, Languages, MonitorSmartphone, Palette, Server } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { Page } from '@/components/page'
import { Button } from '@/components/ui/button'
import { TextField } from '@/components/ui/controls'
import { Row, RowValue, Section } from '@/components/ui/list'
import { Sheet, SheetFooter } from '@/components/ui/sheet'
import { StatusDot } from '@/components/ui/status'
import { IconTile } from '@/components/ui/tile'
import { api, isApiError } from '@/lib/api'
import { errorText } from '@/lib/errors'
import { qk, usePasskeys, useRelay, useSession, useSystem } from '@/lib/queries'
import { relayLook, relayTone } from '@/lib/status'
import { useThemePref } from '@/lib/theme'
import { toast } from '@/lib/toast'
import type { Me, Person } from '@/lib/types'

// Настройки (DESIGN.md §8.9): the person, the home, security, the connection, ChatGPT access, about, sign out.

export function SettingsPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const me = useSession().data
  const passkeys = usePasskeys()
  const relay = useRelay()
  const system = useSystem()
  const theme = useThemePref()
  const [editing, setEditing] = useState(false)
  const look = relay.data ? relayLook(relay.data) : undefined

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
    <Page title={t('settings.title')}>
      {me && (
        <Section>
          <Row
            onClick={() => setEditing(true)}
            className="min-h-[68px] [--row-inset:72px]"
            leading={<Initials name={me.person.name} />}
            title={<span className="type-headline">{me.person.name}</span>}
            subtitle={t(`settings.role.${me.person.role}`)}
            chevron
          />
        </Section>
      )}

      <Section header={t('settings.home')} id="settings-home">
        <Row to="/admin/settings/home" iconInset leading={<IconTile icon={House} color="blue" />} title={t('settings.home.name')} trailing={<RowValue>{me?.home.name}</RowValue>} chevron />
        <Row
          to="/admin/settings/language"
          iconInset
          leading={<IconTile icon={Languages} color="indigo" />}
          title={t('settings.language')}
          trailing={<RowValue>{(me?.person.language ?? me?.home.language) === 'en' ? 'English' : 'Русский'}</RowValue>}
          chevron
        />
        <Row
          to="/admin/settings/appearance"
          iconInset
          leading={<IconTile icon={Palette} color="purple" />}
          title={t('settings.appearance')}
          trailing={<RowValue>{t(`settings.appearance.${theme}`)}</RowValue>}
          chevron
        />
      </Section>

      <Section header={t('settings.security')} id="settings-security">
        <Row
          to="/admin/settings/passkeys"
          iconInset
          leading={<IconTile icon={KeyRound} color="gray" />}
          title={t('settings.passkeys')}
          trailing={<RowValue className="tabular-nums">{passkeys.data?.items.length ?? me?.person.passkeys}</RowValue>}
          chevron
        />
        <Row to="/admin/settings/sessions" iconInset leading={<IconTile icon={MonitorSmartphone} color="teal" />} title={t('settings.sessions')} chevron />
      </Section>

      <Section header={t('settings.connection')} id="settings-connection">
        <Row
          to="/admin/settings/server"
          iconInset
          leading={<IconTile icon={Server} color="green" />}
          title={t('settings.server')}
          trailing={
            look && (
              <span className="inline-flex items-center gap-1.5">
                <StatusDot tone={relayTone(look)} />
                <RowValue>{t(`relay.${look}`)}</RowValue>
              </span>
            )
          }
          chevron
        />
      </Section>

      <Section header={t('settings.keys.section')} id="settings-keys">
        <Row to="/admin/settings/keys" iconInset leading={<IconTile icon={Key} color="orange" />} title={t('settings.keys')} chevron />
      </Section>

      <Section header={t('settings.about')} id="settings-about">
        <Row
          to="/admin/settings/about"
          iconInset
          leading={<IconTile icon={Info} color="gray" />}
          title={t('settings.version')}
          trailing={
            <span className="inline-flex items-center gap-2">
              {system.data?.update && <span aria-hidden className="size-2.5 rounded-full bg-accent" />}
              <RowValue className="tabular-nums">{system.data?.version ?? me?.version}</RowValue>
            </span>
          }
          chevron
        />
      </Section>

      <Section>
        <Row onClick={() => void logout()} title={t('settings.logout')} destructive centered />
      </Section>

      {me && <ProfileSheet open={editing} me={me} onClose={() => setEditing(false)} />}
    </Page>
  )
}

export function Initials({ name, size = 40 }: { name: string; size?: number }) {
  const letters = name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0]?.toUpperCase())
    .join('')
  return (
    <span
      aria-hidden
      className="inline-flex shrink-0 items-center justify-center rounded-full bg-linear-135 from-tile-blue to-tile-indigo font-semibold text-white"
      style={{ width: size, height: size, fontSize: size * 0.4 }}
    >
      {letters || '?'}
    </span>
  )
}

function ProfileSheet({ open, me, onClose }: { open: boolean; me: Me; onClose: () => void }) {
  const { t } = useTranslation()
  return (
    <Sheet open={open} onClose={onClose} title={t('settings.profile.title')}>
      <ProfileForm me={me} onDone={onClose} />
    </Sheet>
  )
}

function ProfileForm({ me, onDone }: { me: Me; onDone: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [name, setName] = useState(me.person.name)
  const [error, setError] = useState<string>()
  const save = useMutation({
    mutationFn: () => api.patch<Person>('/me', { name: name.trim() }),
    onSuccess: (person) => {
      qc.setQueryData<Me>(qk.session, (m) => m && { ...m, person })
      toast(t('common.saved'))
      onDone()
    },
    onError: (err) => setError(isApiError(err, 'invalid_args') ? t('form.maxLength', { max: 40 }) : errorText(err, t)),
  })
  const trimmed = name.trim()
  return (
    <form
      id="profile-form"
      onSubmit={(e) => {
        e.preventDefault()
        if (trimmed.length < 1) return setError(t('form.required'))
        save.mutate()
      }}
    >
      <TextField
        label={t('setup.name.label')}
        hint={t('settings.profile.footer')}
        error={error}
        value={name}
        onChange={(e) => setName(e.target.value)}
        maxLength={40}
        autoComplete="name"
        autoFocusOnOpen
      />
      <SheetFooter>
        <Button type="submit" form="profile-form" variant="filled" size="lg" loading={save.isPending} disabled={trimmed === me.person.name}>
          {t('common.save')}
        </Button>
      </SheetFooter>
    </form>
  )
}
