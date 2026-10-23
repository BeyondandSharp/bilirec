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
import { SizeInput } from '@/components/settings/SizeInput'
import { useLanguage } from '@/hooks/use-language'
import type { ConfigField, LocalizedText } from '@/lib/types'
import { cn } from '@/lib/utils'

/** Pick the active language from a backend-provided three-language string. */
export function localizedText(text: LocalizedText | undefined, language: string, fallback = ''): string {
  if (!text) {
    return fallback
  }
  const value = (text as unknown as Record<string, string>)[language]
  return value ?? text.en ?? text['zh-CN'] ?? fallback
}

/** Short value hint used as placeholder, e.g. the built-in default. */
function placeholderFor(field: ConfigField, t: TFunction): string | undefined {
  if (field.default !== undefined && field.default !== '') {
    return field.default
  }
  switch (field.kind) {
    case 'csv':
      return t('settingsView.system.csvPlaceholder')
    case 'duration':
      return t('settingsView.system.durationPlaceholder')
    default:
      return undefined
  }
}

interface ConfigFieldControlProps {
  field: ConfigField
  value: string
  disabled?: boolean
  onChange: (value: string) => void
  className?: string
}

/**
 * Renders one managed env field from the backend `/config` schema (CONFIG-V2
 * §4/§5). Labels and descriptions come from the schema itself (3 languages).
 */
export function ConfigFieldControl({
  field,
  value,
  disabled = false,
  onChange,
  className,
}: ConfigFieldControlProps) {
  const { t } = useTranslation()
  const { language } = useLanguage()
  const id = `config-${field.key}`
  const label = localizedText(field.label, language, field.key)
  const description = localizedText(field.description, language)
  const fullWidth = field.kind === 'textarea' || field.kind === 'csv'

  let control: ReactNode
  switch (field.kind) {
    case 'bool':
      control = (
        <div className="flex items-center justify-between gap-4 rounded-md border border-border bg-muted/20 px-3 py-2">
          <span className="text-sm text-muted-foreground">{description}</span>
          <Switch
            id={id}
            checked={value === 'true'}
            disabled={disabled}
            onCheckedChange={(checked) => onChange(checked ? 'true' : 'false')}
          />
        </div>
      )
      break
    case 'number':
      control = (
        <Input
          id={id}
          type="number"
          inputMode="numeric"
          min={field.min ?? undefined}
          max={field.max ?? undefined}
          value={value}
          placeholder={placeholderFor(field, t)}
          disabled={disabled}
          className="font-mono"
          onChange={(event) => onChange(event.target.value)}
        />
      )
      break
    case 'size':
      control = (
        <SizeInput
          id={id}
          value={value}
          canonicalUnit={field.size_unit || 'B'}
          disabled={disabled}
          onChange={onChange}
        />
      )
      break
    case 'enum': {
      const options = field.enum ?? []
      const selected = options.includes(value)
        ? value
        : field.default && options.includes(field.default)
          ? field.default
          : options[0]
      control = (
        <Select value={selected} disabled={disabled} onValueChange={(next) => onChange(next)}>
          <SelectTrigger id={id} className="w-full">
            <SelectValue placeholder={placeholderFor(field, t)} />
          </SelectTrigger>
          <SelectContent>
            {options.map((option) => (
              <SelectItem key={option} value={option}>
                {option}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )
      break
    }
    case 'textarea':
      control = (
        <Textarea
          id={id}
          rows={3}
          value={value}
          placeholder={placeholderFor(field, t)}
          disabled={disabled}
          className="resize-y font-mono"
          onChange={(event) => onChange(event.target.value)}
        />
      )
      break
    case 'csv':
      control = (
        <Textarea
          id={id}
          rows={2}
          value={value}
          placeholder={placeholderFor(field, t)}
          disabled={disabled}
          className="resize-y font-mono"
          onChange={(event) => onChange(event.target.value)}
        />
      )
      break
    case 'duration':
    case 'string':
    default:
      control = (
        <Input
          id={id}
          value={value}
          placeholder={placeholderFor(field, t)}
          disabled={disabled}
          className={cn((field.kind === 'duration' || !fullWidth) && 'font-mono')}
          onChange={(event) => onChange(event.target.value)}
        />
      )
      break
  }

  return (
    <div className={cn('space-y-1.5', className)}>
      <Label htmlFor={id}>{label}</Label>
      {field.kind !== 'bool' && description && (
        <p className="text-xs text-muted-foreground">{description}</p>
      )}
      {control}
    </div>
  )
}
