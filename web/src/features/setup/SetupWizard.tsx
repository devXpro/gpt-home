import { useQueryClient } from '@tanstack/react-query'
import { ChevronLeft, Link2Off } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { PasskeyButton, PasskeyGlyph } from '@/components/passkey'
import { Rich } from '@/components/rich'
import { Button } from '@/components/ui/button'
import { TextField } from '@/components/ui/controls'
import { EmptyState, SuccessCheck } from '@/components/ui/feedback'
import { Spinner } from '@/components/ui/spinner'
import { OfflineScreen } from '@/features/shell/AppShell'
import { setLanguage } from '@/i18n'
import { api, isApiError, isOffline } from '@/lib/api'
import { cn } from '@/lib/cn'
import { errorText } from '@/lib/errors'
import { spring } from '@/lib/motion'
import { ceremonyError, createCredential, type CreationOptions } from '@/lib/passkeys'
import { passkeyKind, passkeyName } from '@/lib/platform'
import { qk, useBootstrap } from '@/lib/queries'
import type { Ceremony, Me, TokenInfo } from '@/lib/types'

// The first-run wizard (DESIGN.md §8.2): open the one-time link from the home computer, name the home, name
// yourself, create the passkey. One step per screen.

let captured: string | null | undefined

/** takeToken reads `#t=` once, keeps it in memory and removes it from the address bar (ADMIN-API §3.2). */
function takeToken(): string | null {
  if (captured !== undefined) return captured
  const m = /(?:^#|[#&])t=([^&]+)/.exec(window.location.hash)
  captured = m ? decodeURIComponent(m[1]) : null
  if (window.location.hash) window.history.replaceState(window.history.state, '', window.location.pathname + window.location.search)
  return captured
}

type Step = 'checking' | 'problem' | 'home' | 'name' | 'passkey' | 'done'
type Problem = 'invalid' | 'expired' | 'used' | 'offline' | 'error'
const ORDER: Step[] = ['home', 'name', 'passkey', 'done']

export function SetupWizard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [token] = useState(takeToken)
  const [problem, setProblem] = useState<Problem | undefined>(() => (token ? undefined : 'invalid'))
  const [step, setStep] = useState<Step>(() => (token ? 'checking' : 'problem'))
  const [dir, setDir] = useState(1)
  const [home, setHome] = useState('')
  const [name, setName] = useState('')
  const [fieldError, setFieldError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState<{ text: string; danger?: boolean }>()
  const bootstrap = useBootstrap()
  const lang = bootstrap.data?.language
  useEffect(() => {
    if (lang) setLanguage(lang)
  }, [lang])

  useEffect(() => {
    if (!token) return
    let live = true
    api
      .post<TokenInfo>('/auth/token', { token })
      .then((info) => {
        if (!live) return
        setHome(info.homeName)
        setName(info.personName ?? '')
        setStep('home')
      })
      .catch((err: unknown) => {
        if (!live) return
        setProblem(
          isApiError(err, 'token_expired') ? 'expired' : isApiError(err, 'token_used') ? 'used' : isApiError(err, 'token_invalid') ? 'invalid' : isOffline(err) ? 'offline' : 'error',
        )
        setStep('problem')
      })
    return () => {
      live = false
    }
  }, [token])

  const go = (to: Step) => {
    setDir(ORDER.indexOf(to) >= ORDER.indexOf(step) ? 1 : -1)
    setFieldError(undefined)
    setNote(undefined)
    setStep(to)
  }
  const checkName = (v: string) => {
    const n = v.trim()
    if (n.length < 1) return t('form.required')
    if (n.length > 40) return t('form.maxLength', { max: 40 })
    return undefined
  }

  const create = async () => {
    if (!token) return
    setBusy(true)
    setNote(undefined)
    try {
      const begin = await api.post<Ceremony<CreationOptions>>('/auth/register/begin', { token, personName: name.trim(), homeName: home.trim() })
      const credential = await createCredential(begin.publicKey)
      const me = await api.post<Me>('/auth/register/finish', { ceremonyId: begin.ceremonyId, credential, passkeyName: passkeyName() })
      qc.setQueryData(qk.session, me)
      go('done')
    } catch (err) {
      if (isApiError(err, 'token_expired') || isApiError(err, 'token_used')) {
        setProblem(isApiError(err, 'token_used') ? 'used' : 'expired')
        setStep('problem')
      } else if (isApiError(err, 'passkey_exists') || (!isApiError(err) && ceremonyError(err) === 'exists')) {
        setNote({ text: t('setup.passkey.exists') })
      } else if (!isApiError(err) && !isOffline(err) && ceremonyError(err) === 'cancelled') {
        setNote({ text: t('setup.passkey.cancelled') })
      } else {
        setNote({ text: errorText(err, t), danger: true })
      }
    } finally {
      setBusy(false)
    }
  }

  if (step === 'problem' && problem === 'offline') return <OfflineScreen onRetry={() => window.location.reload()} />

  const index = ORDER.indexOf(step)
  const kind = passkeyKind()
  let content: ReactNode
  let action: ReactNode
  switch (step) {
    case 'checking':
      content = (
        <div className="flex flex-1 flex-col items-center justify-center gap-4 pb-24" role="status">
          <Spinner size="lg" />
          <p className="type-callout text-label-2">{t('setup.checking')}</p>
        </div>
      )
      break
    case 'problem':
      content = (
        <div className="flex flex-1 items-center justify-center pb-16">
          <EmptyState
            icon={Link2Off}
            iconClassName="text-warning"
            title={problem === 'expired' ? t('setup.expired') : problem === 'used' ? t('setup.used') : problem === 'error' ? t('error.generic') : t('setup.invalid')}
            text={<Rich text={t('setup.new_link_hint')} />}
          />
        </div>
      )
      break
    case 'home':
      content = (
        <StepText title={t('setup.home.title')} text={t('setup.home.text')}>
          <TextField
            label={t('setup.home.label')}
            hideLabel
            value={home}
            placeholder={t('setup.home.placeholder')}
            onChange={(e) => setHome(e.target.value)}
            error={fieldError}
            hint={home.trim() ? t('setup.home.preview', { name: home.trim() }) : undefined}
            maxLength={40}
            enterKeyHint="next"
            autoComplete="off"
            autoFocus
          />
        </StepText>
      )
      action = (
        <Button
          type="submit"
          variant="filled"
          size="lg"
          onClick={() => {
            const e = checkName(home)
            if (e) setFieldError(e)
            else go('name')
          }}
        >
          {t('common.next')}
        </Button>
      )
      break
    case 'name':
      content = (
        <StepText title={t('setup.name.title')} text={t('setup.name.text')}>
          <TextField
            label={t('setup.name.label')}
            hideLabel
            value={name}
            onChange={(e) => setName(e.target.value)}
            error={fieldError}
            maxLength={40}
            enterKeyHint="next"
            autoComplete="name"
            autoFocus
          />
        </StepText>
      )
      action = (
        <Button
          type="submit"
          variant="filled"
          size="lg"
          onClick={() => {
            const e = checkName(name)
            if (e) setFieldError(e)
            else go('passkey')
          }}
        >
          {t('common.next')}
        </Button>
      )
      break
    case 'passkey':
      content = (
        <div className="flex flex-1 flex-col items-center pt-10 text-center">
          <PasskeyGlyph className="size-16 text-accent" />
          <h1 className="mt-6 type-title-1">{t(`setup.passkey.title.${kind}`)}</h1>
          <p className="prose-ru mt-3 type-callout text-label-2">{t('setup.passkey.text')}</p>
          {note && (
            <p role="alert" className={cn('mt-6 type-footnote', note.danger ? 'text-danger-text' : 'text-label-2')}>
              {note.text}
            </p>
          )}
        </div>
      )
      action = (
        <PasskeyButton onClick={() => void create()} loading={busy}>
          {t('setup.passkey.button')}
        </PasskeyButton>
      )
      break
    case 'done':
      content = (
        <div className="flex flex-1 flex-col items-center pt-16 text-center">
          <SuccessCheck />
          <h1 className="mt-6 type-title-1">{t('setup.done.title')}</h1>
          <p className="mt-3 type-callout text-label-2">{t('setup.done.text')}</p>
        </div>
      )
      action = (
        <Button variant="filled" size="lg" onClick={() => navigate('/admin', { replace: true })}>
          {t('setup.done.button')}
        </Button>
      )
      break
  }

  const canGoBack = step === 'name' || step === 'passkey'
  return (
    <form
      className="flex min-h-dvh flex-col pt-[env(safe-area-inset-top)]"
      noValidate
      onSubmit={(e) => e.preventDefault()}
    >
      <header className="relative flex h-11 items-center px-2">
        {canGoBack && (
          <button
            type="button"
            onClick={() => go(step === 'passkey' ? 'name' : 'home')}
            aria-label={t('nav.back')}
            className="inline-flex size-11 items-center justify-center rounded-full text-accent-text"
          >
            <ChevronLeft aria-hidden className="size-7" strokeWidth={2.2} />
          </button>
        )}
        {index >= 0 && (
          <div className="absolute left-1/2 flex -translate-x-1/2 gap-2" role="img" aria-label={t('setup.step', { n: index + 1, total: ORDER.length })}>
            {ORDER.map((s, i) => (
              <span key={s} aria-hidden className={cn('size-2 rounded-full transition-colors duration-300', i <= index ? 'bg-accent' : 'bg-fill')} />
            ))}
          </div>
        )}
      </header>
      <main className="flex flex-1 flex-col items-center px-6">
        <div className="flex w-full max-w-[420px] flex-1 flex-col">
          <AnimatePresence mode="wait" initial={false} custom={dir}>
            <motion.div
              key={step}
              custom={dir}
              className="flex flex-1 flex-col"
              variants={{
                enter: (d: number) => ({ x: 24 * d, opacity: 0 }),
                center: { x: 0, opacity: 1, transition: spring.smooth },
                exit: { opacity: 0, transition: { duration: 0.12 } },
              }}
              initial="enter"
              animate="center"
              exit="exit"
            >
              {content}
            </motion.div>
          </AnimatePresence>
        </div>
      </main>
      {action && (
        <footer className="sticky bottom-0 flex justify-center bg-canvas px-6 pt-3 pb-[max(20px,env(safe-area-inset-bottom))]">
          <div className="w-full max-w-[420px]">{action}</div>
        </footer>
      )}
    </form>
  )
}

function StepText({ title, text, children }: { title: string; text: string; children: ReactNode }) {
  return (
    <div className="flex flex-col pt-10">
      <h1 className="type-title-1">{title}</h1>
      <p className="prose-ru mt-2 mb-8 type-callout text-label-2">{text}</p>
      {children}
    </div>
  )
}
