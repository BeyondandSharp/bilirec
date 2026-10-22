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
    <div className="flex h-9 shrink-0 items-center overflow-hidden rounded-lg border border-border bg-background">
      <Label htmlFor={id} className="sr-only">
        {label}
      </Label>
      <Select value={value} onValueChange={onValueChange} disabled={disabled}>
        <SelectTrigger id={id} className="h-full w-32 rounded-none border-0 bg-transparent px-2.5 text-xs shadow-none focus:ring-0">
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
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="h-full w-8 shrink-0 rounded-none border-l border-border"
        aria-label={t('viewControls.sortDirection')}
        aria-pressed={direction === 'desc'}
        title={direction === 'desc' ? t('viewControls.descending') : t('viewControls.ascending')}
        disabled={disabled}
        onClick={() => onDirectionChange(direction === 'asc' ? 'desc' : 'asc')}
      >
        {direction === 'desc' ? <ArrowDownIcon size={14} /> : <ArrowUpIcon size={14} />}
      </Button>
    </div>
  )
}
