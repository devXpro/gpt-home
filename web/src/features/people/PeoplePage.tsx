import { Users } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Page } from '@/components/page'
import { EmptyState } from '@/components/ui/feedback'
import { useSession } from '@/lib/queries'

// Люди: a placeholder until phase 3, when T3.4 builds the People screens (DESIGN.md §8.10) in this folder.
export function PeoplePage() {
  const { t } = useTranslation()
  const home = useSession().data?.home.name ?? ''
  return (
    <Page title={t('tab.people')}>
      <EmptyState icon={Users} title={t('people.soon.title')} text={t('people.soon.text', { home })} />
    </Page>
  )
}
