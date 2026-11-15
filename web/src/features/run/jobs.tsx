import { useQueryClient } from '@tanstack/react-query'
import { TriangleAlert } from 'lucide-react'
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { ProgressBar, StepList, SuccessCheck, type StepItem } from '@/components/ui/feedback'
import { Sheet, SheetFooter } from '@/components/ui/sheet'
import { currentLang } from '@/i18n'
import { jobBus, useEventsConnected } from '@/lib/events'
import { fmtBytes } from '@/lib/format'
import { qk, useJob } from '@/lib/queries'
import { toast } from '@/lib/toast'
import { jobErrorText } from '@/lib/errors'
import type { Job } from '@/lib/types'

// Jobs (ADMIN-API §2 Job): installs, updates, removals and the hub's self-update, shown as a StepList
// (DESIGN.md §8.6, §8.9). Closing the sheet keeps the job running; the pill reopens it.

/** jobSteps turns a job into StepList rows, with the download bar under the active download step. */
export function JobSteps({ job }: { job: Job }) {
  const { t } = useTranslation()
  const lang = currentLang()
  const current = job.step ? job.steps.indexOf(job.step) : -1
  const steps: StepItem[] = job.steps.map((s, i) => {
    let state: StepItem['state'] = 'pending'
    if (job.state === 'ok' || i < current) state = 'done'
    else if (i === current) state = job.state === 'failed' ? 'failed' : 'running'
    let detail: ReactNode
    if (state === 'running' && s === 'download' && job.progress?.total) {
      detail = (
        <div className="flex flex-col gap-1.5">
          <ProgressBar value={job.progress.done / job.progress.total} />
          <span className="type-footnote text-label-2 tabular-nums">
            {t('job.downloaded', { done: fmtBytes(job.progress.done, lang), total: fmtBytes(job.progress.total, lang) })}
          </span>
        </div>
      )
    }
    if (state === 'running' && s === 'restart') detail = <span className="type-footnote text-label-2">{t('settings.update.restarting')}</span>
    return { key: s, label: t(`job.step.${s}`, { defaultValue: s }), state, detail }
  })
  return <StepList steps={steps} />
}

export interface JobRequest {
  jobId: string
  title: string
  doneText?: string
  /** runs once when the job ends well (navigate away after a removal, …) */
  onDone?: (job: Job) => void
  /** starts the same job again after a failure */
  retry?: () => Promise<Job | undefined>
}

interface JobsApi {
  openJob: (r: JobRequest) => void
  shownJobId?: string
}

const JobContext = createContext<JobsApi | null>(null)

export function useJobs() {
  const ctx = useContext(JobContext)
  if (!ctx) throw new Error('useJobs outside JobProvider')
  return ctx
}

let seq = 0

export function JobProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const { t } = useTranslation()
  const [state, setState] = useState<{ req: JobRequest; open: boolean; key: number } | null>(null)
  const onDone = useRef(new Map<string, NonNullable<JobRequest['onDone']>>())
  const shownRef = useRef<{ id?: string; open: boolean }>({ open: false })
  useEffect(() => {
    shownRef.current = { id: state?.req.jobId, open: !!state?.open }
  }, [state])

  const openJob = useCallback((req: JobRequest) => {
    if (req.onDone) onDone.current.set(req.jobId, req.onDone)
    setState({ req, open: true, key: ++seq })
  }, [])

  useEffect(
    () =>
      jobBus.on((j) => {
        if (j.state === 'running') return
        const done = onDone.current.get(j.id)
        onDone.current.delete(j.id)
        if (j.state === 'ok') done?.(j)
        const visible = shownRef.current.open && shownRef.current.id === j.id
        if (!visible && done) toast(t(j.state === 'ok' ? 'common.done' : 'run.failed'), j.state === 'ok' ? 'success' : 'warning')
      }),
    [t],
  )

  const value = useMemo(() => ({ openJob, shownJobId: state?.open ? state.req.jobId : undefined }), [openJob, state])
  const close = () => setState((s) => s && { ...s, open: false })

  return (
    <JobContext.Provider value={value}>
      {children}
      <Sheet open={!!state?.open} onClose={close} title={state?.req.title}>
        {state && (
          <JobContent
            key={state.key}
            req={state.req}
            onClose={close}
            onRetry={async () => {
              const next = await state.req.retry?.()
              if (next) {
                qc.setQueryData(qk.job(next.id), next)
                openJob({ ...state.req, jobId: next.id })
              }
            }}
          />
        )}
      </Sheet>
    </JobContext.Provider>
  )
}

function JobContent({ req, onClose, onRetry }: { req: JobRequest; onClose: () => void; onRetry: () => Promise<void> }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const job = useJob(req.jobId).data
  const sse = useEventsConnected()
  const [retrying, setRetrying] = useState(false)
  useEffect(() => {
    if (sse || job?.state !== 'running') return
    const id = setInterval(() => void qc.invalidateQueries({ queryKey: qk.job(req.jobId) }), 1500)
    return () => clearInterval(id)
  }, [sse, job?.state, qc, req.jobId])

  if (!job) return null
  if (job.state === 'ok') {
    return (
      <>
        <div className="flex flex-col items-center pt-4 pb-2 text-center">
          <SuccessCheck />
          <h3 className="mt-4 type-title-3">{req.doneText ?? t('common.done')}</h3>
        </div>
        <SheetFooter>
          <Button variant="filled" size="lg" onClick={onClose}>
            {t('common.done')}
          </Button>
        </SheetFooter>
      </>
    )
  }
  return (
    <>
      <div className="pt-2 pb-2">
        <JobSteps job={job} />
        {job.state === 'failed' && (
          <div role="alert" className="mt-6 flex gap-3 rounded-lg bg-fill p-4">
            <TriangleAlert aria-hidden className="size-6 shrink-0 text-warning" />
            <p className="type-body">{jobErrorText(job, t)}</p>
          </div>
        )}
      </div>
      {job.state === 'failed' && (
        <SheetFooter>
          {req.retry && (
            <Button
              variant="filled"
              size="lg"
              loading={retrying}
              onClick={async () => {
                setRetrying(true)
                try {
                  await onRetry()
                } finally {
                  setRetrying(false)
                }
              }}
            >
              {t('common.retry')}
            </Button>
          )}
          <Button variant="plain" size="lg" onClick={onClose}>
            {t('common.close')}
          </Button>
        </SheetFooter>
      )}
    </>
  )
}
