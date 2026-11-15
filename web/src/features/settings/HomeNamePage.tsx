import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { Page } from '@/components/page'
import { Button } from '@/components/ui/button'
import { TextField } from '@/components/ui/controls'
import { api, isApiError } from '@/lib/api'
import { errorText } from '@/lib/errors'
import { qk, useSession } from '@/lib/queries'
import { toast } from '@/lib/toast'
import type { Bootstrap, Me, Overview, Settings } from '@/lib/types'


// The home's name (DESIGN.md §8.9): what the family sees in ChatGPT.

export function HomeNamePage() {
  const { t } = useTranslation()
  const me = useSession().data
  return (
    <Page title={t('settings.home.name')} back={{ to: '/admin/settings', label: t('settings.title') }}>
      {me && <HomeNameForm current={me.home.name} />}
    </Page>
  )
}

function HomeNameForm({ current }: { current: string }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [name, setName] = useState(current)
  const [error, setError] = useState<string>()
  const trimmed = name.trim()
  const save = useMutation({
    mutationFn: () => api.patch<Settings>('/settings', { homeName: trimmed }),
    onSuccess: (s) => {
      qc.setQueryData(qk.settings, s)
      qc.setQueryData<Me>(qk.session, (m) => m && { ...m, home: { ...m.home, name: s.homeName } })
      qc.setQueryData<Bootstrap>(qk.bootstrap, (b) => b && { ...b, homeName: s.homeName })
      qc.setQueryData<Overview>(qk.overview, (o) => o && { ...o, home: { name: s.homeName } })
      toast(t('common.saved'))
      navigate('/admin/settings')
    },
    onError: (err) => setError(isApiError(err, 'invalid_args') ? t('form.maxLength', { max: 40 }) : errorText(err, t)),
  })
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        if (!trimmed) return setError(t('form.required'))
        save.mutate()
      }}
      className="flex flex-col gap-6"
    >
      <TextField
        label={t('setup.home.label')}
        hint={trimmed ? t('setup.home.preview', { name: trimmed }) : undefined}
        error={error}
        value={name}
        onChange={(e) => {
          setName(e.target.value)
          setError(undefined)
        }}
        maxLength={40}
        autoComplete="off"
        enterKeyHint="done"
      />
      <p className="-mt-3 px-1 type-footnote text-label-2">{t('settings.home.footer')}</p>
      <Button type="submit" variant="filled" size="lg" loading={save.isPending} disabled={trimmed === current || !trimmed}>
        {t('common.save')}
      </Button>
    </form>
  )
}
