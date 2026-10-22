import { useTranslation } from 'react-i18next'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'

interface GroupToggleProps {
  id: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  disabled?: boolean
}

export function GroupToggle({ id, checked, onCheckedChange, disabled }: GroupToggleProps) {
  const { t } = useTranslation()
  return (
    <div className="flex items-center gap-2 pb-1">
      <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} disabled={disabled} />
      <Label htmlFor={id} className="text-sm font-normal text-muted-foreground">
        {t('viewControls.groupByStatus')}
      </Label>
    </div>
  )
}
