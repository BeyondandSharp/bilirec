import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
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
import { SecretsSection } from '@/components/settings/SecretsSection'
import { SystemConfigSection } from '@/components/settings/SystemConfigSection'
import { apiClient } from '@/lib/api'
import { getServerErrorMessage } from '@/lib/api-error'
import { useRole } from '@/lib/role-context'
import {
  cloneSettings,
  getSettingByPath,
  setSettingByPath,
  SETTINGS_SECTIONS,
} from '@/lib/settings-schema'
import type { GlobalSettings, SettingsResponse } from '@/lib/types'

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
  const [systemDirty, setSystemDirty] = useState(false)
  const [isSavingAll, setIsSavingAll] = useState(false)
  const systemSaveRef = useRef<(() => Promise<void>) | null>(null)

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

  const handleSave = async (): Promise<boolean> => {
    if (!draft || isReadOnly) {
      return false
    }
    setIsSaving(true)
    setActionError(null)
    try {
      const response = await apiClient.updateSettings(draft)
      setData(response)
      setDraft(cloneSettings(response.values))
      toast.success(t('settingsView.saveSuccess'), { position: 'bottom-center' })
      return true
    } catch (error) {
      console.error('Failed to save settings:', error)
      const message = getServerErrorMessage(error, t('settingsView.saveFailed'))
      setActionError(message)
      toast.error(message, { position: 'bottom-center' })
      return false
    } finally {
      setIsSaving(false)
    }
  }

  const handleSaveAll = async () => {
    if (isReadOnly || isSavingAll) {
      return
    }
    setIsSavingAll(true)
    try {
      if (isDirty) {
        const ok = await handleSave()
        if (!ok) {
          return
        }
      }
      if (systemDirty) {
        await systemSaveRef.current?.()
      }
    } finally {
      setIsSavingAll(false)
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
            {data?.config_file && (
              <p className="mt-1 font-mono text-xs text-muted-foreground">
                {t('settingsView.system.configFile')}: {data.config_file}
              </p>
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
          <>
            <section aria-labelledby="settings-recording-title" className="space-y-3">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0 space-y-1">
                  <h3 id="settings-recording-title" className="font-semibold">
                    {t('settingsView.recordingTitle')}
                  </h3>
                  <p className="text-sm text-muted-foreground">{t('settingsView.description')}</p>
                </div>
                {!isReadOnly && (
                  <div className="flex shrink-0 items-center gap-2">
                    <Button
                      variant="outline"
                      onClick={() => setIsResetDialogOpen(true)}
                      disabled={actionsDisabled || !data}
                    >
                      {isResetting ? t('settingsView.resetting') : t('settingsView.reset')}
                    </Button>
                  </div>
                )}
              </div>

              {SETTINGS_SECTIONS.map((section) => (
                <section
                  key={section.id}
                  aria-labelledby={`settings-section-${section.id}`}
                  className="rounded-xl border border-border bg-card p-4 shadow-sm"
                >
                  <div className="mb-3 space-y-1">
                    <h4 id={`settings-section-${section.id}`} className="font-semibold">
                      {t(section.titleKey)}
                    </h4>
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
              ))}
            </section>

            <SystemConfigSection onDirtyChange={setSystemDirty} saveHandlerRef={systemSaveRef} />
            <SecretsSection />
          </>
        )}
      </div>

      {(isDirty || systemDirty) && !isReadOnly && (
        <div className="sticky bottom-0 z-20 border-t border-border bg-background/95 px-4 py-3 shadow-[0_-4px_12px_rgba(0,0,0,0.06)] backdrop-blur supports-[backdrop-filter]:bg-background/80">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="text-sm text-muted-foreground">{t('settingsView.unsavedChanges')}</p>
            <Button onClick={() => void handleSaveAll()} disabled={isSavingAll}>
              {isSavingAll ? t('settingsView.savingAll') : t('settingsView.saveAll')}
            </Button>
          </div>
        </div>
      )}

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
