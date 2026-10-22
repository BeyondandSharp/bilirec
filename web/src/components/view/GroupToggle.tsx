import { ArrowsSplitIcon } from '@phosphor-icons/react'
import { useTranslation } from 'react-i18next'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'

interface GroupToggleProps {
  id: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  disabled?: boolean
}

export function GroupToggle({ id, checked, onCheckedChange, disabled }: GroupToggleProps) {
  const { t } = useTranslation()
  return (
    <div
      className={cn(
        'flex h-9 shrink-0 items-center gap-2 rounded-lg border border-border bg-background px-2.5 text-xs',
        checked ? 'border-primary/40 bg-primary/5 text-primary' : 'text-muted-foreground',
      )}
    >
      <ArrowsSplitIcon size={14} />
      <Label htmlFor={id} className="cursor-pointer text-xs font-normal">
        {t('viewControls.groupByStatus')}
      </Label>
      <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} disabled={disabled} className="scale-90" />
    </div>
  )
}
