import type { TFunction } from 'i18next'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { SizeInput, sizeToCanonical } from '@/components/settings/SizeInput'
import {
  formatSettingValue,
  type SettingsFieldDescriptor,
} from '@/lib/settings-schema'
import { cn } from '@/lib/utils'

interface SettingsFieldControlProps {
  id: string
  field: SettingsFieldDescriptor
  /** Current (draft) value; undefined/null on a room override means "not overridden". */
  value: unknown
  /** Effective global/default value, used for placeholders and inherited display. */
  fallbackValue?: unknown
  /** True when the room override inherits the global value (controls disabled). */
  inherit?: boolean
  disabled?: boolean
  onChange: (value: unknown) => void
  className?: string
}

function toStringList(value: unknown): string {
  return Array.isArray(value) ? value.join('\n') : ''
}

function parseStringList(raw: string): string[] {
  return raw
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
}

/** Placeholder used when a text/number input is empty; prefers the global value. */
function placeholderFor(
  field: SettingsFieldDescriptor,
  fallbackValue: unknown,
  t: TFunction,
): string | undefined {
  const formatted = formatSettingValue(field, fallbackValue, t)
  if (formatted !== '—') {
    return formatted
  }
  return field.placeholderKey ? t(field.placeholderKey) : undefined
}

/**
 * Renders one settings field control from a {@link SettingsFieldDescriptor}.
 * Used by both the global SettingsView (edit draft) and RoomConfigDialog
 * (per-room override, where an absent value means "inherit global").
 */
export function SettingsFieldControl({
  id,
  field,
  value,
  fallbackValue,
  inherit = false,
  disabled = false,
  onChange,
  className,
}: SettingsFieldControlProps) {
  const { t } = useTranslation()
  const label = t(field.labelKey)
  const hint = field.hintKey ? t(field.hintKey) : undefined
  const isDisabled = disabled
  const globalValueLabel = field.kind === 'boolean' || field.kind === 'enum'
    ? t('roomConfig.globalValue', {
        value: formatSettingValue(field, fallbackValue, t),
      })
    : undefined

  if (field.kind === 'boolean') {
    const effectiveValue = inherit ? fallbackValue : value ?? fallbackValue
    return (
      <div
        className={cn(
          'flex items-center justify-between gap-4 rounded-lg border border-border bg-muted/20 p-3',
          className,
        )}
      >
        <div className="min-w-0 space-y-1">
          <Label htmlFor={id}>{label}</Label>
          {hint && <p className="text-sm text-muted-foreground">{hint}</p>}
          {inherit && globalValueLabel && (
            <p className="text-xs text-muted-foreground">{globalValueLabel}</p>
          )}
        </div>
        <Switch
          id={id}
          checked={effectiveValue === true}
          disabled={isDisabled}
          onCheckedChange={(checked) => onChange(checked)}
        />
      </div>
    )
  }

  let control: ReactNode
  switch (field.kind) {
    case 'enum': {
      const selectValue = String(inherit ? fallbackValue ?? '' : value ?? fallbackValue ?? '')
      control = (
        <Select value={selectValue} disabled={isDisabled} onValueChange={(next) => onChange(next)}>
          <SelectTrigger id={id} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {field.options?.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {t(option.labelKey)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )
      break
    }
    case 'number': {
      const displayValue = inherit
        ? fallbackValue ?? ''
        : value === undefined || value === null
          ? ''
          : value
      control = (
        <Input
          id={id}
          type="number"
          inputMode="numeric"
          min={field.min}
          max={field.max}
          value={String(displayValue)}
          placeholder={placeholderFor(field, fallbackValue, t)}
          disabled={isDisabled}
          className={cn(field.monospace && 'font-mono')}
          onChange={(event) => {
            const raw = event.target.value
            if (raw === '') {
              onChange(undefined)
              return
            }
            const parsed = Number(raw)
            onChange(Number.isFinite(parsed) ? parsed : undefined)
          }}
        />
      )
      break
    }
    case 'size': {
      const displayValue = inherit ? fallbackValue ?? '' : value ?? fallbackValue ?? ''
      control = (
        <SizeInput
          id={id}
          value={String(displayValue)}
          canonicalUnit="MiB"
          disabled={isDisabled}
          onChange={(next) => {
            const parsed = sizeToCanonical(next, 'MiB')
            onChange(parsed === undefined ? undefined : Math.round(parsed))
          }}
        />
      )
      break
    }
    case 'string': {
      const displayValue = inherit ? fallbackValue ?? '' : value ?? ''
      control = (
        <Input
          id={id}
          value={String(displayValue)}
          placeholder={placeholderFor(field, fallbackValue, t)}
          disabled={isDisabled}
          className={cn(field.monospace && 'font-mono')}
          onChange={(event) => onChange(event.target.value)}
        />
      )
      break
    }
    case 'textarea':
    case 'string-list': {
      const displayValue = inherit ? fallbackValue : value
      const text = field.kind === 'string-list'
        ? toStringList(displayValue)
        : typeof displayValue === 'string'
          ? displayValue
          : ''
      const placeholder = placeholderFor(field, fallbackValue, t)
      control = (
        <Textarea
          id={id}
          rows={field.kind === 'string-list' ? 3 : 4}
          value={text}
          placeholder={placeholder}
          disabled={isDisabled}
          className={cn(field.monospace && 'font-mono', 'resize-y')}
          onChange={(event) => {
            if (field.kind === 'string-list') {
              onChange(parseStringList(event.target.value))
            } else {
              onChange(event.target.value)
            }
          }}
        />
      )
      break
    }
    default:
      control = null
  }

  return (
    <div className={cn('space-y-1.5', className)}>
      <Label htmlFor={id}>{label}</Label>
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
      {control}
    </div>
  )
}
