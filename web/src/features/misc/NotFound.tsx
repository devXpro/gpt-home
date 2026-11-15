import { useTranslation } from 'react-i18next'
import { Page } from '@/components/page'
import { ButtonLink } from '@/components/ui/button'
import { ErrorState } from '@/components/ui/feedback'

// Unknown routes (DESIGN.md §8.12.4).

function Body() {
  const { t } = useTranslation()
  return (
    <ErrorState
      title={t('notfound.title')}
      action={
        <ButtonLink to="/admin" variant="tinted" size="lg">
          {t('common.home')}
        </ButtonLink>
      }
    />
  )
}

/** NotFoundPage is the 404 inside the signed-in shell. */
export function NotFoundPage() {
  const { t } = useTranslation()
  return (
    <Page title={t('notfound.title')} largeTitle={false}>
      <Body />
    </Page>
  )
}

/** NotFoundScreen is the 404 outside /admin (e.g. /join before phase 3). */
export function NotFoundScreen() {
  return (
    <main className="flex min-h-dvh items-center justify-center px-6">
      <Body />
    </main>
  )
}
