import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  CaretDownIcon,
  CaretRightIcon,
  MagnifyingGlassIcon,
  WarningCircleIcon,
} from '@phosphor-icons/react'
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
import { LoadingScreen } from '@/components/LoadingScreen'
import { ConfigFieldControl, localizedText } from '@/components/settings/ConfigFieldControl'
import { apiClient } from '@/lib/api'
import { getServerErrorMessage } from '@/lib/api-error'
import { useConvertCapability } from '@/lib/convert-capability'
import { useRole } from '@/lib/role-context'
import { useLanguage } from '@/hooks/use-language'
import type { ConfigField, ConfigGroup, ConfigSource, SystemConfigResponse } from '@/lib/types'

/** Groups owned by other sections of the settings page (FEATURES-V2 form / secrets form). */
const EXCLUDED_GROUP_IDS = new Set(['features', 'secrets'])

const SOURCE_LABEL_KEYS: Record<ConfigSource, string> = {
  env: 'settingsView.system.sourceEnv',
  file: 'settingsView.system.sourceFile',
  dotenv: 'settingsView.system.sourceDotenv',
  default: 'settingsView.system.sourceDefault',
  'secret-file': 'settingsView.system.sourceSecretFile',
}

const SOURCE_CLASSES: Record<ConfigSource, string> = {
  env: 'border-amber-500/50 text-amber-700 dark:text-amber-400',
  file: 'border-sky-500/50 text-sky-700 dark:text-sky-400',
  dotenv: 'border-violet-500/50 text-violet-700 dark:text-violet-400',
  default: 'text-muted-foreground',
  'secret-file': 'border-rose-500/50 text-rose-700 dark:text-rose-400',
}

function SourceBadge({ source }: { source: ConfigSource | undefined }) {
  const { t } = useTranslation()
  const value = source ?? 'default'
  return (
    <Badge variant="outline" className={`text-xs font-normal ${SOURCE_CLASSES[value]}`}>
      {t(SOURCE_LABEL_KEYS[value])}
    </Badge>
  )
}

/**
 * System config rendered dynamically from `GET /config` schema (CONFIG-V2 §4/§6):
 * folded groups, search, source badges, env-locked fields, restart-required
 * badges and fields that need unavailable tooling (e.g. ffmpeg).
 */
export function SystemConfigSection() {
  const { t } = useTranslation()
  const { language } = useLanguage()
  const { isReadOnly } = useRole()
  const { canConvert } = useConvertCapability()
  const [data, setData] = useState<SystemConfigResponse | null>(null)
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)
  const [isResetting, setIsResetting] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set())
  const [isResetDialogOpen, setIsResetDialogOpen] = useState(false)

  const loadConfig = useCallback(async () => {
    setIsLoading(true)
    setLoadError(null)
    try {
      const response = await apiClient.getConfig()
      setData(response)
      setDraft({ ...response.values })
    } catch (error) {
      console.error('Failed to load system config:', error)
      setLoadError(getServerErrorMessage(error, t('settingsView.system.loadFailed')))
    } finally {
      setIsLoading(false)
    }
  }, [t])

  useEffect(() => {
    void loadConfig()
  }, [loadConfig])

  const groups = useMemo(
    () => (data?.schema?.groups ?? []).filter((group) => !EXCLUDED_GROUP_IDS.has(group.id)),
    [data],
  )

  const query = search.trim().toLowerCase()

  const matchesField = useCallback(
    (field: ConfigField): boolean => {
      if (!query) {
        return true
      }
      const texts = [
        field.key,
        field.label?.en,
        field.label?.['zh-CN'],
        field.label?.['zh-TW'],
        field.description?.en,
        field.description?.['zh-CN'],
        field.description?.['zh-TW'],
      ]
      return texts.some((text) => typeof text === 'string' && text.toLowerCase().includes(query))
    },
    [query],
  )

  const visibleGroups = useMemo(() => {
    return groups
      .map((group: ConfigGroup) => {
        const groupTexts = [
          group.label?.en,
          group.label?.['zh-CN'],
          group.label?.['zh-TW'],
          group.description?.en,
          group.description?.['zh-CN'],
          group.description?.['zh-TW'],
        ]
        const groupMatches = Boolean(
          query && groupTexts.some((text) => typeof text === 'string' && text.toLowerCase().includes(query)),
        )
        const fields = groupMatches ? group.fields : group.fields.filter(matchesField)
        return { group, fields }
      })
      .filter((entry) => entry.fields.length > 0)
  }, [groups, matchesField, query])

  const dirtyKeys = useMemo(() => {
    if (!data) {
      return []
    }
    return Object.keys(data.values).filter((key) => draft[key] !== data.values[key])
  }, [data, draft])

  const isBusy = isLoading || isSaving || isResetting

  const isFieldLocked = (field: ConfigField): boolean => {
    const source = data?.sources?.[field.key]
    return source === 'env'
  }

  const isFieldUnavailable = (field: ConfigField): boolean => {
    return field.requires === 'ffmpeg' && !canConvert
  }

  const isFieldDisabled = (field: ConfigField): boolean => {
    return isReadOnly || isBusy || isFieldLocked(field) || isFieldUnavailable(field)
  }

  const handleSave = async () => {
    if (!data || isReadOnly || dirtyKeys.length === 0) {
      return
    }
    setIsSaving(true)
    setActionError(null)
    try {
      const values: Record<string, string> = {}
      for (const key of dirtyKeys) {
        values[key] = draft[key] ?? ''
      }
      const response = await apiClient.updateConfig({ values })
      setData(response)
      setDraft({ ...response.values })
      toast.success(t('settingsView.system.saveSuccess'), { position: 'bottom-center' })
    } catch (error) {
      console.error('Failed to save system config:', error)
      const message = getServerErrorMessage(error, t('settingsView.system.saveFailed'))
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
      await apiClient.resetConfig()
      const response = await apiClient.getConfig()
      setData(response)
      setDraft({ ...response.values })
      setIsResetDialogOpen(false)
      toast.success(t('settingsView.system.resetSuccess'), { position: 'bottom-center' })
    } catch (error) {
      console.error('Failed to reset system config:', error)
      const message = getServerErrorMessage(error, t('settingsView.system.resetFailed'))
      setActionError(message)
      toast.error(message, { position: 'bottom-center' })
    } finally {
      setIsResetting(false)
    }
  }

  const toggleGroup = (groupId: string) => {
    setExpandedGroups((current) => {
      const next = new Set(current)
      if (next.has(groupId)) {
        next.delete(groupId)
      } else {
        next.add(groupId)
      }
      return next
    })
  }

  const restartLabels = (data?.restart_required ?? []).map((key) => {
    const field = groups.flatMap((group) => group.fields).find((item) => item.key === key)
    return field ? localizedText(field.label, language, key) : key
  })

  return (
    <section
      aria-labelledby="settings-system-title"
      className="space-y-3 rounded-xl border border-border bg-card p-4 shadow-sm"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <h3 id="settings-system-title" className="font-semibold">
            {t('settingsView.system.title')}
          </h3>
          <p className="text-sm text-muted-foreground">{t('settingsView.system.description')}</p>
          {data?.config_file && (
            <p className="font-mono text-xs text-muted-foreground">
              {t('settingsView.system.configFile')}: {data.config_file}
            </p>
          )}
        </div>
        {!isReadOnly && (
          <div className="flex shrink-0 items-center gap-2">
            <Button
              variant="outline"
              onClick={() => setIsResetDialogOpen(true)}
              disabled={isBusy || !data}
            >
              {isResetting ? t('settingsView.system.resetting') : t('settingsView.system.reset')}
            </Button>
            <Button
              onClick={() => void handleSave()}
              disabled={isBusy || !data || dirtyKeys.length === 0}
            >
              {isSaving ? t('settingsView.system.saving') : t('settingsView.system.save')}
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

      {restartLabels.length > 0 && (
        <Alert>
          <WarningCircleIcon size={16} />
          <AlertDescription>
            {t('settingsView.system.restartRequiredNotice', { keys: restartLabels.join(', ') })}
          </AlertDescription>
        </Alert>
      )}

      {isLoading ? (
        <LoadingScreen />
      ) : loadError ? (
        <div className="space-y-3">
          <Alert variant="destructive">
            <WarningCircleIcon size={16} />
            <AlertTitle>{t('settingsView.system.loadFailed')}</AlertTitle>
            <AlertDescription>{loadError}</AlertDescription>
          </Alert>
          <Button variant="outline" onClick={() => void loadConfig()}>
            {t('settingsView.system.retry')}
          </Button>
        </div>
      ) : !data ? (
        <Alert>
          <WarningCircleIcon size={16} />
          <AlertDescription>{t('settingsView.system.loadFailed')}</AlertDescription>
        </Alert>
      ) : (
        <>
          <div className="relative w-full sm:max-w-sm">
            <MagnifyingGlassIcon
              size={16}
              className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              value={search}
              placeholder={t('settingsView.system.searchPlaceholder')}
              aria-label={t('settingsView.system.searchAria')}
              className="pl-8"
              onChange={(event) => setSearch(event.target.value)}
            />
          </div>

          {visibleGroups.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">
              {t('settingsView.system.noResults')}
            </p>
          ) : (
            <div className="space-y-2">
              {visibleGroups.map(({ group, fields }) => {
                const expanded = query.length > 0 || expandedGroups.has(group.id)
                const label = localizedText(group.label, language, group.id)
                return (
                  <div key={group.id} className="overflow-hidden rounded-lg border border-border">
                    <button
                      type="button"
                      aria-expanded={expanded}
                      aria-label={t(
                        expanded ? 'settingsView.system.collapseGroup' : 'settingsView.system.expandGroup',
                        { group: label },
                      )}
                      onClick={() => toggleGroup(group.id)}
                      className="flex w-full cursor-pointer items-center gap-2 bg-muted/30 px-3 py-2 text-left transition-colors hover:bg-muted/50"
                    >
                      {expanded ? <CaretDownIcon size={14} /> : <CaretRightIcon size={14} />}
                      <span className="text-sm font-medium">{label}</span>
                      <span className="text-xs text-muted-foreground">
                        {t('settingsView.system.fieldsCount', { count: group.fields.length })}
                      </span>
                    </button>
                    {expanded && (
                      <div className="grid gap-3 border-t border-border p-3 sm:grid-cols-2">
                        {fields.map((field) => {
                          const locked = isFieldLocked(field)
                          const unavailable = isFieldUnavailable(field)
                          const source = data.sources?.[field.key]
                          return (
                            <div
                              key={field.key}
                              className={
                                field.kind === 'textarea' || field.kind === 'csv'
                                  ? 'space-y-1.5 rounded-lg border border-border/60 p-2.5 sm:col-span-2'
                                  : 'space-y-1.5 rounded-lg border border-border/60 p-2.5'
                              }
                            >
                              <ConfigFieldControl
                                field={field}
                                value={draft[field.key] ?? ''}
                                disabled={isFieldDisabled(field)}
                                onChange={(value) =>
                                  setDraft((current) => ({ ...current, [field.key]: value }))
                                }
                              />
                              <div className="flex flex-wrap items-center gap-1.5">
                                <SourceBadge source={source} />
                                {field.restart_required && (
                                  <Badge variant="secondary" className="text-xs font-normal">
                                    {t('settingsView.system.restartBadge')}
                                  </Badge>
                                )}
                                {locked && (
                                  <Badge
                                    variant="outline"
                                    className="text-xs font-normal"
                                    title={t('settingsView.system.envLockedHint')}
                                  >
                                    {t('settingsView.system.envLocked')}
                                  </Badge>
                                )}
                                {unavailable && (
                                  <span className="text-xs text-muted-foreground">
                                    {field.disabled_reason || t('settingsView.system.requiresFfmpeg')}
                                  </span>
                                )}
                              </div>
                            </div>
                          )
                        })}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </>
      )}

      <AlertDialog open={isResetDialogOpen} onOpenChange={setIsResetDialogOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('settingsView.system.resetConfirmTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('settingsView.system.resetConfirmDescription')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isResetting}>
              {t('settingsView.system.cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={isResetting}
              onClick={(event) => {
                event.preventDefault()
                void handleReset()
              }}
            >
              {isResetting ? t('settingsView.system.resetting') : t('settingsView.system.resetConfirm')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}
