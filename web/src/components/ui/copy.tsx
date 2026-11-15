import { Check, Copy } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { copyText } from '@/lib/clipboard'
import { cn } from '@/lib/cn'
import { Button } from './button'
import { ActionSheet } from './sheet'

/** useCopy copies and flips its icon to a check for 1.5 s. */
function useCopy() {
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (!copied) return
    const id = setTimeout(() => setCopied(false), 1500)
    return () => clearTimeout(id)
  }, [copied])
  const copy = async (text: string) => {
    const ok = await copyText(text)
    if (ok) setCopied(true)
    return ok
  }
  return { copied, copy }
}

/** CopyField is a monospace value in a --fill box with "Скопировать" (DESIGN.md §6). */
export function CopyField({
  value,
  label,
  onCopied,
  className,
}: {
  value: string
  label?: string
  onCopied?: () => void
  className?: string
}) {
  const { t } = useTranslation()
  const { copied, copy } = useCopy()
  return (
    <div className={cn('flex items-center gap-2 rounded-md bg-fill py-1.5 pr-1.5 pl-3.5', className)}>
      <code aria-label={label} className="min-w-0 flex-1 font-mono text-[14px] leading-5 break-all text-label select-all">
        {value}
      </code>
      <button
        type="button"
        onClick={async () => {
          if (await copy(value)) onCopied?.()
        }}
        className="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-full bg-accent-tint px-3 text-[14px] font-semibold text-accent-tinted"
      >
        {copied ? <Check aria-hidden className="size-4" strokeWidth={2.5} /> : <Copy aria-hidden className="size-4" />}
        <span aria-live="polite">{copied ? t('common.copied') : t('common.copy')}</span>
      </button>
    </div>
  )
}

/** SecretOnce shows a secret exactly once: copy it, then "Готово" asks again if it was never copied. */
export function SecretOnce({ value, onDone }: { value: string; onDone: () => void }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const [asking, setAsking] = useState(false)
  return (
    <div className="flex flex-col gap-3">
      <CopyField value={value} onCopied={() => setCopied(true)} />
      <p className="px-1 type-footnote text-warning-text">{t('settings.keys.once')}</p>
      <Button variant="filled" size="lg" className="mt-2" onClick={() => (copied ? onDone() : setAsking(true))}>
        {t('common.done')}
      </Button>
      <ActionSheet
        open={asking}
        title={t('settings.keys.sure')}
        text={t('settings.keys.sure.text')}
        action={t('settings.keys.sure.yes')}
        destructive={false}
        onConfirm={() => {
          setAsking(false)
          onDone()
        }}
        onCancel={() => setAsking(false)}
      />
    </div>
  )
}
