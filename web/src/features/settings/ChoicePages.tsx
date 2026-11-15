import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Page } from '@/components/page'
import { CheckList } from '@/components/ui/controls'
import { Section } from '@/components/ui/list'
import { setLanguage } from '@/i18n'
import { api } from '@/lib/api'
import { errorText } from '@/lib/errors'
import { qk, useSession } from '@/lib/queries'
import { setThemePref, useThemePref, type ThemePref } from '@/lib/theme'
import { toast } from '@/lib/toast'
import type { Lang, Me, Person, Settings } from '@/lib/types'

// Язык and Оформление (DESIGN.md §8.9): checkmarked lists that apply at once.

export function LanguagePage() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const me = useSession().data
  const current: Lang = me?.person.language ?? me?.home.language ?? 'ru'

  const choose = async (lang: Lang) => {
    if (!me || lang === current) return
    // Switch at once (no reload); the person's choice and, for the owner, the home's default follow on the server.
    setLanguage(lang)
    qc.setQueryData<Me>(qk.session, (m) => m && { ...m, person: { ...m.person, language: lang } })
    try {
      const person = await api.patch<Person>('/me', { language: lang })
      qc.setQueryData<Me>(qk.session, (m) => m && { ...m, person })
      if (me.person.role === 'owner') {
        const s = await api.patch<Settings>('/settings', { language: lang })
        qc.setQueryData(qk.settings, s)
      }
    } catch (err) {
      setLanguage(current)
      qc.setQueryData<Me>(qk.session, (m) => m && { ...m, person: { ...m.person, language: current } })
      toast(errorText(err, t), 'warning')
    }
  }

  return (
    <Page title={t('settings.language')} back={{ to: '/admin/settings', label: t('settings.title') }}>
      <Section footer={t('settings.language.footer')}>
        <CheckList
          label={t('settings.language')}
          options={[
            { value: 'ru', label: 'Русский' },
            { value: 'en', label: 'English' },
          ]}
          selected={[current]}
          onToggle={(v) => void choose(v as Lang)}
        />
      </Section>
    </Page>
  )
}

export function AppearancePage() {
  const { t } = useTranslation()
  const pref = useThemePref()
  return (
    <Page title={t('settings.appearance')} back={{ to: '/admin/settings', label: t('settings.title') }}>
      <Section>
        <CheckList<ThemePref>
          label={t('settings.appearance')}
          options={[
            { value: 'dark', label: t('settings.appearance.dark') },
            { value: 'light', label: t('settings.appearance.light') },
            { value: 'system', label: t('settings.appearance.system') },
          ]}
          selected={[pref]}
          onToggle={setThemePref}
        />
      </Section>
    </Page>
  )
}
