import { useCallback, useEffect, useMemo, useState } from 'react'
import { isAxiosError } from 'axios'
import { InfoIcon, WarningCircleIcon } from '@phosphor-icons/react'
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
import { LoadingScreen } from '@/components/LoadingScreen'
import { SettingsFieldControl } from '@/components/settings/SettingsFieldControl'
import { apiClient } from '@/lib/api'
import { useRole } from '@/lib/role-context'
import {
  cloneSettings,
  getSettingByPath,
  setSettingByPath,
  SETTINGS_SECTIONS,
} from '@/lib/settings-schema'
import type { GlobalSettings, SettingsResponse } from '@/lib/types'

/** Extract a human-readable message from an API failure (server 400s carry Chinese text). */
function getServerErrorMessage(error: unknown, fallback: string): string {
  if (isAxiosError(error)) {
    const data = error.response?.data
    if (typeof data === 'string' && data.trim()) {
      return data.trim()
    }
    if (data && typeof data === 'object') {
      const record = data as Record<string, unknown>
      const message = record.message ?? record.error ?? record.msg
      if (typeof message === 'string' && message.trim()) {
        return message.trim()
      }
    }
    if (error.response?.status) {
      return `${fallback} (HTTP ${error.response.status})`
    }
  }
  return fallback
}

/**
 * Global settings tab (FEATURES-V2 §7, R17): load `GET /settings`, edit and
 * save (`PUT /settings`, admin), reset persisted overrides. Viewers get a
 * read-only view. Settings only apply to newly started recording sessions.
 */
export function SettingsView() {
  const { t } = useTranslation()
  const { isReadOnly } = useRole()
  const [data, setData] = useState<SettingsResponse | null>(null)
  const [draft, setDraft] = useState<GlobalSettings | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)
  const [isResetting, setIsResetting] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [isResetDialogOpen, setIsResetDialogOpen] = useState(false)

  const loadSettings = useCallback(async () => {
    setIsLoading(true)
    setLoadError(null)
    try {
      const response = await apiClient.getSettings()
      setData(response)
      setDraft(cloneSettings(response.values))
    } catch (error) {
      console.error('Failed to load settings:', error)
      setLoadError(getServerErrorMessage(error, t('settingsView.loadFailed')))
    } finally {
      setIsLoading(false)
    }
  }, [t])

  useEffect(() => {
    void loadSettings()
  }, [loadSettings])

  const isDirty = useMemo(() => {
    if (!data || !draft) {
      return false
    }
    return JSON.stringify(draft) !== JSON.stringify(data.values)
  }, [data, draft])

  const updateField = (fieldPath: string, value: unknown) => {
    setDraft((current) => {
      if (!current) {
        return current
      }
      // Numbers are always present in the global payload; clearing an input
      // falls back to 0 so the saved object keeps every key (partial PUT
      // semantics would otherwise leave the previous override in place).
      return setSettingByPath(current, fieldPath, value === undefined ? 0 : value)
    })
  }

  const handleSave = async () => {
    if (!draft || isReadOnly) {
      return
    }
    setIsSaving(true)
    setActionError(null)
    try {
      const response = await apiClient.updateSettings(draft)
      setData(response)
      setDraft(cloneSettings(response.values))
      toast.success(t('settingsView.saveSuccess'), { position: 'bottom-center' })
    } catch (error) {
      console.error('Failed to save settings:', error)
      const message = getServerErrorMessage(error, t('settingsView.saveFailed'))
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
      await apiClient.resetSettings()
      const response = await apiClient.getSettings()
      setData(response)
      setDraft(cloneSettings(response.values))
      setIsResetDialogOpen(false)
      toast.success(t('settingsView.resetSuccess'), { position: 'bottom-center' })
    } catch (error) {
      console.error('Failed to reset settings:', error)
      const message = getServerErrorMessage(error, t('settingsView.resetFailed'))
      setActionError(message)
      toast.error(message, { position: 'bottom-center' })
    } finally {
      setIsResetting(false)
    }
  }

  const isBusy = isLoading || isSaving || isResetting
  const actionsDisabled = isReadOnly || isBusy

  return (
    <div className="flex h-full flex-col">
      <div className="sticky top-0 z-10 border-b border-border bg-background p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 className="text-xl font-bold">{t('settingsView.title')}</h2>
            <p className="text-sm text-muted-foreground">{t('settingsView.description')}</p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {!isReadOnly && (
              <>
                <Button
                  variant="outline"
                  onClick={() => setIsResetDialogOpen(true)}
                  disabled={actionsDisabled || !data}
                >
                  {isResetting ? t('settingsView.resetting') : t('settingsView.reset')}
                </Button>
                <Button
                  onClick={() => void handleSave()}
                  disabled={actionsDisabled || !draft || !isDirty}
                >
                  {isSaving ? t('settingsView.saving') : t('settingsView.save')}
                </Button>
              </>
            )}
          </div>
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Badge variant="secondary" className="gap-1.5 font-normal">
            <InfoIcon size={14} />
            {t('settingsView.newSessionNotice')}
          </Badge>
          {isReadOnly && (
            <p className="text-sm text-muted-foreground">{t('settingsView.readOnlyNotice')}</p>
          )}
        </div>
      </div>

      <div className="flex-1 space-y-4 overflow-y-auto p-4 pb-20">
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
              <AlertTitle>{t('settingsView.loadFailed')}</AlertTitle>
              <AlertDescription>{loadError}</AlertDescription>
            </Alert>
            <Button variant="outline" onClick={() => void loadSettings()}>
              {t('settingsView.retry')}
            </Button>
          </div>
        ) : !data || !draft ? (
          <Alert>
            <WarningCircleIcon size={16} />
            <AlertDescription>{t('settingsView.loadFailed')}</AlertDescription>
          </Alert>
        ) : (
          SETTINGS_SECTIONS.map((section) => (
            <section
              key={section.id}
              aria-labelledby={`settings-section-${section.id}`}
              className="rounded-xl border border-border bg-card p-4 shadow-sm"
            >
              <div className="mb-3 space-y-1">
                <h3 id={`settings-section-${section.id}`} className="font-semibold">
                  {t(section.titleKey)}
                </h3>
                {section.descriptionKey && (
                  <p className="text-sm text-muted-foreground">{t(section.descriptionKey)}</p>
                )}
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                {section.fields.map((field) => (
                  <SettingsFieldControl
                    key={field.path}
                    id={`settings-${field.path.split('.').join('-')}`}
                    field={field}
                    value={getSettingByPath(draft, field.path)}
                    fallbackValue={getSettingByPath(data.defaults, field.path)}
                    disabled={isReadOnly || isBusy}
                    className={field.fullWidth ? 'sm:col-span-2' : undefined}
                    onChange={(value) => updateField(field.path, value)}
                  />
                ))}
              </div>
            </section>
          ))
        )}
      </div>

      <AlertDialog open={isResetDialogOpen} onOpenChange={setIsResetDialogOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('settingsView.resetConfirmTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('settingsView.resetConfirmDescription')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isResetting}>
              {t('settingsView.cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={isResetting}
              onClick={(event) => {
                event.preventDefault()
                void handleReset()
              }}
            >
              {isResetting ? t('settingsView.resetting') : t('settingsView.resetConfirm')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
