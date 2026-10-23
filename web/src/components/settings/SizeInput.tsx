import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

const UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'] as const
type SizeUnit = (typeof UNITS)[number]
const FACTORS: Record<SizeUnit, number> = {
  B: 1,
  KiB: 1024,
  MiB: 1024 ** 2,
  GiB: 1024 ** 3,
  TiB: 1024 ** 4,
  PiB: 1024 ** 5,
}

interface ParsedSize {
  amount: number
  unit: SizeUnit
  bytes: number
}

function parseSize(raw: string, canonicalUnit: string): ParsedSize {
  const value = String(raw ?? '').trim()
  const match = /^([0-9]+(?:\.[0-9]+)?)\s*([A-Za-z]+)?$/.exec(value)
  const fallbackUnit = (UNITS.find((unit) => unit.toLowerCase() === canonicalUnit.toLowerCase()) ?? 'B') as SizeUnit
  if (!match) {
    return { amount: 0, unit: fallbackUnit, bytes: 0 }
  }
  const amount = Number(match[1])
  const rawUnit = match[2]?.toLowerCase()
  const unit = (UNITS.find((candidate) => candidate.toLowerCase() === rawUnit) ?? fallbackUnit) as SizeUnit
  return { amount, unit, bytes: amount * FACTORS[unit] }
}

function formatAmount(amount: number): string {
  return Number.isInteger(amount) ? String(amount) : String(Math.round(amount * 1000) / 1000)
}

/** Convert a size string to the canonical unit (default MiB) as a number. */
export function sizeToCanonical(raw: string, canonicalUnit = 'MiB'): number | undefined {
  const parsed = parseSize(raw, canonicalUnit)
  if (!raw.trim()) {
    return undefined
  }
  const unit = (UNITS.find((candidate) => candidate.toLowerCase() === canonicalUnit.toLowerCase()) ?? 'B') as SizeUnit
  return parsed.bytes / FACTORS[unit]
}

interface SizeInputProps {
  id: string
  value: string
  canonicalUnit?: string
  disabled?: boolean
  onChange: (value: string) => void
}

/** Numeric size with B/KiB/MiB/GiB/TiB/PiB units; persisted as "5 GiB". */
export function SizeInput({ id, value, canonicalUnit = 'B', disabled, onChange }: SizeInputProps) {
  const { t } = useTranslation()
  const initial = parseSize(value, canonicalUnit)
  const [amount, setAmount] = useState(formatAmount(initial.amount))
  const [unit, setUnit] = useState<SizeUnit>(initial.unit)
  const lastValue = useRef(value)

  useEffect(() => {
    if (value === lastValue.current) {
      return
    }
    const parsed = parseSize(value, canonicalUnit)
    setAmount(formatAmount(parsed.amount))
    setUnit(parsed.unit)
    lastValue.current = value
  }, [canonicalUnit, value])

  const emit = (nextAmount: string, nextUnit: SizeUnit) => {
    const normalized = nextAmount.trim()
    const next = normalized === '' ? '' : `${normalized} ${nextUnit}`
    lastValue.current = next
    onChange(next)
  }

  const changeUnit = (nextUnit: SizeUnit) => {
    // Do not convert the number on the frontend: keep the typed amount and
    // let the backend interpret/convert when the value is used.
    setUnit(nextUnit)
    emit(amount, nextUnit)
  }

  return (
    <div className="flex gap-2">
      <Input
        id={id}
        type="number"
        inputMode="decimal"
        min={0}
        step="any"
        value={amount}
        disabled={disabled}
        className="min-w-0 flex-1 font-mono"
        onChange={(event) => {
          setAmount(event.target.value)
          emit(event.target.value, unit)
        }}
      />
      <Select value={unit} onValueChange={(next) => changeUnit(next as SizeUnit)} disabled={disabled}>
        <SelectTrigger className="w-24 shrink-0" aria-label={t('settingsView.system.sizeUnit')}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {UNITS.map((candidate) => (
            <SelectItem key={candidate} value={candidate}>
              {candidate}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}
