import { useCallback, useEffect, useMemo, useState } from 'react'
import { WarningCircleIcon } from '@phosphor-icons/react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LoadingScreen } from '@/components/LoadingScreen'
import { localizedText } from '@/components/settings/ConfigFieldControl'
import { apiClient } from '@/lib/api'
import { getServerErrorMessage } from '@/lib/api-error'
import { useRole } from '@/lib/role-context'
import { useLanguage } from '@/hooks/use-language'
import type { ConfigField, SecretStatusResponse } from '@/lib/types'

/** Secrets managed by the backend (CONFIG-V2 §1); served by GET /config/secrets set flags. */
const FALLBACK_SECRET_KEYS = [
  'PASSWORD',
  'VIEWER_PASSWORD',
  'JWT_SECRET',
  'CLOUDCONVERT_API_KEY',
  'FRP_TOKEN',
  'NOTIFY_SSE_TOKEN',
] as const

const FALLBACK_LABEL_KEYS: Record<string, string> = {
  PASSWORD: 'settingsView.secrets.secretPassword',
  VIEWER_PASSWORD: 'settingsView.secrets.secretViewerPassword',
  JWT_SECRET: 'settingsView.secrets.secretJwtSecret',
  CLOUDCONVERT_API_KEY: 'settingsView.secrets.secretCloudconvertApiKey',
  FRP_TOKEN: 'settingsView.secrets.secretFrpToken',
  NOTIFY_SSE_TOKEN: 'settingsView.secrets.secretNotifySseToken',
}

interface SecretEntry {
  key: string
  label: string
  description: string
}

/**
 * Write-only secrets editor (CONFIG-V2 §6): GET /config/secrets exposes only
 * set/unset flags, so values can be replaced or cleared but never read back.
 */
export function SecretsSection() {
  const { t } = useTranslation()
  const { language } = useLanguage()
  const { isReadOnly } = useRole()
  const [data, setData] = useState<SecretStatusResponse | null>(null)
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [markedForClear, setMarkedForClear] = useState<Set<string>>(new Set())
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)
  const [isResetting, setIsResetting] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [isResetDialogOpen, setIsResetDialogOpen] = useState(false)

  const loadSecrets = useCallback(async () => {
    setIsLoading(true)
    setLoadError(null)
    try {
      const response = await apiClient.getSecretStatus()
      setData(response)
      setDraft({})
      setMarkedForClear(new Set())
    } catch (error) {
      console.error('Failed to load secret status:', error)
      setLoadError(getServerErrorMessage(error, t('settingsView.secrets.loadFailed')))
    } finally {
      setIsLoading(false)
    }
  }, [t])

  useEffect(() => {
    void loadSecrets()
  }, [loadSecrets])

  const entries = useMemo<SecretEntry[]>(() => {
    const fromSchema = (data?.schema?.groups ?? []).flatMap((group) => group.fields)
    const schemaFields: ConfigField[] = (fromSchema.length > 0 ? fromSchema : data?.fields ?? [])
      .filter((field) => field.kind === 'secret' || field.sensitive || fromSchema.length > 0)
    if (schemaFields.length > 0) {
      return schemaFields.map((field) => ({
        key: field.key,
        label: localizedText(field.label, language, field.key),
        description: localizedText(field.description, language),
      }))
    }
    const keys = new Set<string>([...FALLBACK_SECRET_KEYS, ...Object.keys(data?.set ?? {})])
    return [...keys].map((key) => ({
      key,
      label: FALLBACK_LABEL_KEYS[key] ? t(FALLBACK_LABEL_KEYS[key]) : key,
      description: '',
    }))
  }, [data, language, t])

  const hasChanges = useMemo(() => {
    const typed = Object.values(draft).some((value) => value.trim().length > 0)
    return typed || markedForClear.size > 0
  }, [draft, markedForClear])

  const isBusy = isLoading || isSaving || isResetting

  const toggleClear = (key: string) => {
    setMarkedForClear((current) => {
      const next = new Set(current)
      if (next.has(key)) {
        next.delete(key)
      } else {
        next.add(key)
      }
      return next
    })
  }

  const handleSave = async () => {
    if (!data || isReadOnly || !hasChanges) {
      return
    }
    setIsSaving(true)
    setActionError(null)
    try {
      const values: Record<string, string> = {}
      for (const [key, value] of Object.entries(draft)) {
        if (value.trim().length > 0) {
          values[key] = value
        }
      }
      const clear = [...markedForClear].filter((key) => !values[key])
      await apiClient.updateSecrets({ values, clear })
      await loadSecrets()
      toast.success(t('settingsView.secrets.saveSuccess'), { position: 'bottom-center' })
    } catch (error) {
      console.error('Failed to save secrets:', error)
      const message = getServerErrorMessage(error, t('settingsView.secrets.saveFailed'))
      setActionError(message)
      toast.error(message, { position: 'bottom-center' })
    } finally {
      setIsSaving(false)
    }
  }

  const handleReset = async () => {
    if (isReadOnly) {
      return
    }
    setIsResetting(true)
    setActionError(null)
    try {
      await apiClient.resetSecrets()
      await loadSecrets()
      setIsResetDialogOpen(false)
      toast.success(t('settingsView.secrets.resetSuccess'), { position: 'bottom-center' })
    } catch (error) {
      console.error('Failed to clear secrets:', error)
      const message = getServerErrorMessage(error, t('settingsView.secrets.resetFailed'))
      setActionError(message)
      toast.error(message, { position: 'bottom-center' })
    } finally {
      setIsResetting(false)
    }
  }

  return (
    <section
      aria-labelledby="settings-secrets-title"
      className="space-y-3 rounded-xl border border-border bg-card p-4 shadow-sm"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <h3 id="settings-secrets-title" className="font-semibold">
            {t('settingsView.secrets.title')}
          </h3>
          <p className="text-sm text-muted-foreground">{t('settingsView.secrets.description')}</p>
          {(data?.fields?.length ?? 0) + (data?.schema?.groups?.length ?? 0) > 0 ? null : (
            <p className="text-xs text-muted-foreground">{t('settingsView.secrets.writeOnlyHint')}</p>
          )}
        </div>
        {!isReadOnly && (
          <div className="flex shrink-0 items-center gap-2">
            <Button
              variant="outline"
              onClick={() => setIsResetDialogOpen(true)}
              disabled={isBusy || !data}
            >
              {isResetting ? t('settingsView.secrets.resetting') : t('settingsView.secrets.reset')}
            </Button>
            <Button
              onClick={() => void handleSave()}
              disabled={isBusy || !data || !hasChanges}
            >
              {isSaving ? t('settingsView.secrets.saving') : t('settingsView.secrets.save')}
            </Button>
          </div>
        )}
      </div>

      {actionError && (
        <Alert variant="destructive">
          <WarningCircleIcon size={16} />
          <AlertTitle>{t('settingsView.actionErrorTitle')}</AlertTitle>
          <AlertDescription>{actionError}</AlertDescription>
        </Alert>
      )}

      {isLoading ? (
        <LoadingScreen />
      ) : loadError ? (
        <div className="space-y-3">
          <Alert variant="destructive">
            <WarningCircleIcon size={16} />
            <AlertTitle>{t('settingsView.secrets.loadFailed')}</AlertTitle>
            <AlertDescription>{loadError}</AlertDescription>
          </Alert>
          <Button variant="outline" onClick={() => void loadSecrets()}>
            {t('settingsView.secrets.retry')}
          </Button>
        </div>
      ) : !data ? (
        <Alert>
          <WarningCircleIcon size={16} />
          <AlertDescription>{t('settingsView.secrets.loadFailed')}</AlertDescription>
        </Alert>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2">
          {entries.map((entry) => {
            const isSet = data.set?.[entry.key] === true
            const willClear = markedForClear.has(entry.key)
            return (
              <div key={entry.key} className="space-y-1.5 rounded-lg border border-border/60 p-2.5">
                <div className="flex items-center justify-between gap-2">
                  <Label htmlFor={`secret-${entry.key}`}>{entry.label}</Label>
                  <Badge
                    variant={isSet ? 'secondary' : 'outline'}
                    className="text-xs font-normal"
                  >
                    {isSet ? t('settingsView.secrets.set') : t('settingsView.secrets.unset')}
                  </Badge>
                </div>
                {entry.description && (
                  <p className="text-xs text-muted-foreground">{entry.description}</p>
                )}
                <Input
                  id={`secret-${entry.key}`}
                  type="password"
                  autoComplete="new-password"
                  value={draft[entry.key] ?? ''}
                  placeholder={t('settingsView.secrets.placeholder')}
                  disabled={isReadOnly || isBusy || willClear}
                  onChange={(event) =>
                    setDraft((current) => ({ ...current, [entry.key]: event.target.value }))
                  }
                />
                <div className="flex items-center justify-between gap-2">
                  <p className="text-xs text-muted-foreground">{t('settingsView.secrets.writeOnlyHint')}</p>
                  {isSet && !isReadOnly && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-7 shrink-0 text-xs"
                      onClick={() => toggleClear(entry.key)}
                    >
                      {willClear ? t('settingsView.secrets.cancelClear') : t('settingsView.secrets.clear')}
                    </Button>
                  )}
                </div>
                {willClear && (
                  <Badge variant="destructive" className="text-xs font-normal">
                    {t('settingsView.secrets.clearSet')}
                  </Badge>
                )}
              </div>
            )
          })}
        </div>
      )}

      <AlertDialog open={isResetDialogOpen} onOpenChange={setIsResetDialogOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('settingsView.secrets.resetConfirmTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('settingsView.secrets.resetConfirmDescription')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isResetting}>
              {t('settingsView.secrets.cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={isResetting}
              onClick={(event) => {
                event.preventDefault()
                void handleReset()
              }}
            >
              {isResetting
                ? t('settingsView.secrets.resetting')
                : t('settingsView.secrets.resetConfirm')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}
