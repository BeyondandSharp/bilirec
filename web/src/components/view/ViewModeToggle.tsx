import { ListIcon, SquaresFourIcon } from '@phosphor-icons/react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'

export type ViewMode = 'grid' | 'list'

interface ViewModeToggleProps {
  value: ViewMode
  onChange: (value: ViewMode) => void
  disabled?: boolean
}

export function ViewModeToggle({ value, onChange, disabled }: ViewModeToggleProps) {
  const { t } = useTranslation()
  return (
    <div className="flex items-center rounded-md border border-border p-0.5" role="group" aria-label={t('viewControls.layout')}>
      <Button
        type="button"
        variant={value === 'grid' ? 'secondary' : 'ghost'}
        size="icon"
        className="size-8"
        aria-label={t('viewControls.grid')}
        aria-pressed={value === 'grid'}
        disabled={disabled}
        onClick={() => onChange('grid')}
      >
        <SquaresFourIcon size={16} />
      </Button>
      <Button
        type="button"
        variant={value === 'list' ? 'secondary' : 'ghost'}
        size="icon"
        className="size-8"
        aria-label={t('viewControls.list')}
        aria-pressed={value === 'list'}
        disabled={disabled}
        onClick={() => onChange('list')}
      >
        <ListIcon size={16} />
      </Button>
    </div>
  )
}
