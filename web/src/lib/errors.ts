// Error codes → words (DESIGN.md §10.6); unknown codes use error.generic.

import type { TFunction } from 'i18next'
import { isApiError, isOffline } from './api'
import type { Job } from './types'

type T = TFunction

export function errorText(err: unknown, t: T): string {
  if (isOffline(err)) return t('error.offline')
  if (isApiError(err)) {
    if (err.code === 'tool_failed' && err.message) return err.message
    return t(`error.${err.code}`, { defaultValue: t('error.generic') })
  }
  return t('error.generic')
}

/** jobErrorText words a failed job (DESIGN.md §10.5). */
export function jobErrorText(job: Job, t: T): string {
  const code = job.error?.code ?? 'internal'
  return t(`job.error.${code}`, { defaultValue: t(`error.${code}`, { defaultValue: t('error.generic') }) })
}
