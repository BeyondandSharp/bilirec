import { ArrowDownIcon, ArrowUpIcon } from '@phosphor-icons/react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

export type SortDirection = 'asc' | 'desc'

interface SortOption {
  value: string
  label: string
}

interface SortControlProps {
  id: string
  label: string
  value: string
  options: SortOption[]
  direction: SortDirection
  onValueChange: (value: string) => void
  onDirectionChange: (direction: SortDirection) => void
  disabled?: boolean
}

export function SortControl({
  id,
  label,
  value,
  options,
  direction,
  onValueChange,
  onDirectionChange,
  disabled,
}: SortControlProps) {
  const { t } = useTranslation()
  return (
    <div className="flex items-end gap-1.5">
      <div className="min-w-0 space-y-1">
        <Label htmlFor={id} className="text-xs text-muted-foreground">
          {label}
        </Label>
        <Select value={value} onValueChange={onValueChange} disabled={disabled}>
          <SelectTrigger id={id} className="w-full min-w-32 sm:w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {options.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <Button
        type="button"
        variant="outline"
        size="icon"
        className="size-9 shrink-0"
        aria-label={t('viewControls.sortDirection')}
        aria-pressed={direction === 'desc'}
        title={direction === 'desc' ? t('viewControls.descending') : t('viewControls.ascending')}
        disabled={disabled}
        onClick={() => onDirectionChange(direction === 'asc' ? 'desc' : 'asc')}
      >
        {direction === 'desc' ? <ArrowDownIcon size={16} /> : <ArrowUpIcon size={16} />}
      </Button>
    </div>
  )
}
