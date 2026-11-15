import { useQueryClient } from '@tanstack/react-query'
import { House } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Navigate, useNavigate, useSearchParams } from 'react-router'
import { PasskeyButton } from '@/components/passkey'
import { EmptyState, Skeleton } from '@/components/ui/feedback'
import { AppIcon } from '@/components/ui/tile'
import { OfflineScreen } from '@/features/shell/AppShell'
import { setLanguage } from '@/i18n'
import { api, isApiError, isOffline } from '@/lib/api'
import { cn } from '@/lib/cn'
import { errorText } from '@/lib/errors'
import { useNow } from '@/lib/hooks'
import { ceremonyError, getAssertion, webauthnSupported, type RequestOptions } from '@/lib/passkeys'
import { passkeyKind } from '@/lib/platform'
import { qk, useBootstrap, useSession } from '@/lib/queries'
import type { Ceremony, Me } from '@/lib/types'

// Sign in (DESIGN.md §8.1): the home's name and one button. Passkeys only; no user name (discoverable credentials).

/** safeNext keeps the return path inside the admin, so ?next= cannot send the person elsewhere. */
function safeNext(v: string | null) {
  return v && v.startsWith('/admin') && !v.startsWith('/admin/login') && !v.includes('//') ? v : '/admin'
}

type State = 'idle' | 'waiting' | 'cancelled' | 'failed' | 'limited' | 'error'

export function LoginPage() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [search] = useSearchParams()
  const next = safeNext(search.get('next'))
  const bootstrap = useBootstrap()
  const session = useSession()
  const [state, setState] = useState<State>('idle')
  const [error, setError] = useState('')
  const [retryAt, setRetryAt] = useState(0)
  const now = useNow(1000)
  const kind = passkeyKind()

  const lang = bootstrap.data?.language
  useEffect(() => {
    if (lang) setLanguage(lang)
  }, [lang])

  if (session.data) return <Navigate to={next} replace />
  if (bootstrap.error && isOffline(bootstrap.error)) return <OfflineScreen onRetry={() => void bootstrap.refetch()} />

  const limited = state === 'limited' && now < retryAt
  const login = async () => {
    setState('waiting')
    try {
      const begin = await api.post<Ceremony<RequestOptions>>('/auth/login/begin', {})
      const credential = await getAssertion(begin.publicKey)
      const me = await api.post<Me>('/auth/login/finish', { ceremonyId: begin.ceremonyId, credential })
      qc.setQueryData(qk.session, me)
      navigate(next, { replace: true })
    } catch (err) {
      if (isApiError(err, 'login_failed')) setState('failed')
      else if (isApiError(err, 'rate_limited')) {
        setRetryAt(Date.now() + (err.retryAfter ?? 60) * 1000)
        setState('limited')
      } else if (!isApiError(err) && !isOffline(err) && ceremonyError(err) === 'cancelled') setState('cancelled')
      else {
        setError(errorText(err, t))
        setState('error')
      }
    }
  }

  const b = bootstrap.data
  let body
  if (b && !b.ownerReady) {
    body = <EmptyState icon={House} title={t('login.not_set_up.title')} text={t('login.not_set_up')} />
  } else if (!webauthnSupported()) {
    body = <p className="mt-8 text-center type-body text-label-2">{t('login.unsupported')}</p>
  } else {
    const note: { text: string; tone: 'muted' | 'danger' } =
      state === 'cancelled'
        ? { text: t('login.cancelled'), tone: 'muted' }
        : state === 'failed'
          ? { text: t('login.failed'), tone: 'danger' }
          : limited
            ? { text: t('error.rate_limited'), tone: 'danger' }
            : state === 'error'
              ? { text: error, tone: 'danger' }
              : { text: t('login.footnote'), tone: 'muted' }
    body = (
      <div className="mt-10 flex w-full flex-col items-center">
        <PasskeyButton onClick={() => void login()} loading={state === 'waiting'} disabled={!b || limited}>
          {state === 'waiting' ? t('login.waiting') : t('login.button')}
        </PasskeyButton>
        <p role={note.tone === 'danger' ? 'alert' : undefined} className={cn('mt-4 min-h-[36px] text-center type-footnote', note.tone === 'danger' ? 'text-danger-text' : 'text-label-2')}>
          {note.text}
        </p>
      </div>
    )
  }

  return (
    <main className="flex min-h-dvh flex-col items-center justify-center px-6 pt-[env(safe-area-inset-top)] pb-[max(12vh,env(safe-area-inset-bottom))]">
      <div className="flex w-full max-w-[360px] flex-col items-center text-center">
        <AppIcon size={72} />
        {b ? <h1 className="mt-5 type-title-1 text-balance">{b.homeName}</h1> : <Skeleton className="mt-6 h-8 w-48" />}
        {(!b || b.ownerReady) && <p className="mt-1.5 type-callout text-label-2">{t(`login.subtitle.${kind}`)}</p>}
        {body}
      </div>
    </main>
  )
}
