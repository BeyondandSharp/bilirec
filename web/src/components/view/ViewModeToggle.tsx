import { ListIcon, SquaresFourIcon } from '@phosphor-icons/react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

export type ViewMode = 'grid' | 'list'

interface ViewModeToggleProps {
  value: ViewMode
  onChange: (value: ViewMode) => void
  disabled?: boolean
}

export function ViewModeToggle({ value, onChange, disabled }: ViewModeToggleProps) {
  const { t } = useTranslation()
  return (
    <div
      className="flex h-9 shrink-0 items-center rounded-lg border border-border bg-background p-0.5"
      role="group"
      aria-label={t('viewControls.layout')}
    >
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className={cn('size-7 rounded-md', value === 'grid' && 'bg-primary/10 text-primary hover:bg-primary/15')}
        aria-label={t('viewControls.grid')}
        aria-pressed={value === 'grid'}
        disabled={disabled}
        onClick={() => onChange('grid')}
      >
        <SquaresFourIcon size={15} />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className={cn('size-7 rounded-md', value === 'list' && 'bg-primary/10 text-primary hover:bg-primary/15')}
        aria-label={t('viewControls.list')}
        aria-pressed={value === 'list'}
        disabled={disabled}
        onClick={() => onChange('list')}
      >
        <ListIcon size={15} />
      </Button>
    </div>
  )
}
