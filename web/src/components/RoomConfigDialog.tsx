import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { RecordStartOptions } from '@/components/RecordStartOptions'
import { SettingsFieldControl } from '@/components/settings/SettingsFieldControl'
import { apiClient } from '@/lib/api'
import type { GlobalSettings, RoomConfig, RoomInfo, UpdateRoomConfigRequest } from '@/lib/types'
import {
  FALLBACK_GLOBAL_SETTINGS,
  SETTINGS_SECTIONS,
  buildRoomOverridePayload,
  clearGroupOverrides,
  deriveInheritGroups,
  getSettingByPath,
  setSettingByPath,
  type SettingsSectionDescriptor,
  type SettingsSectionId,
} from '@/lib/settings-schema'
import { toast } from 'sonner'
import { useTranslation } from 'react-i18next'
import { mutate } from 'swr'

interface RoomConfigDialogProps {
  roomInfo: RoomInfo
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function RoomConfigDialog({ roomInfo, open, onOpenChange }: RoomConfigDialogProps) {
  const { t } = useTranslation()
  const [isLoading, setIsLoading] = useState(false)
  const [isSaving, setIsSaving] = useState(false)
  const [roomConfig, setRoomConfig] = useState<RoomConfig | null>(null)
  const [globalSettings, setGlobalSettings] = useState<GlobalSettings>(FALLBACK_GLOBAL_SETTINGS)
  /** Inherit global per group (FEATURES-V2 §1.3: nil override = inherit). */
  const [inheritByGroup, setInheritByGroup] = useState<Record<SettingsSectionId, boolean>>({
    segment: true,
    naming: true,
    cover: true,
    danmaku: true,
    flv: true,
    bili: true,
  })

  useEffect(() => {
    if (!open) {
      return
    }

    let isMounted = true

    const loadRoomConfig = async () => {
      setIsLoading(true)
      try {
        const config = await apiClient.getRoomConfig(roomInfo.room_id)
        // Global values only feed placeholders/inherited previews: an older
        // backend without GET /settings must not block room config editing.
        let globalValues: GlobalSettings = FALLBACK_GLOBAL_SETTINGS
        try {
          const settings = await apiClient.getSettings()
          if (settings?.values) {
            globalValues = settings.values
          }
        } catch (settingsError) {
          console.warn('Failed to load global settings for room overrides:', settingsError)
        }
        if (isMounted) {
          setRoomConfig(config)
          setGlobalSettings(globalValues)
          setInheritByGroup(deriveInheritGroups(config))
        }
      } catch (error: any) {
        console.error('Failed to load room config:', error)
        toast.error(error.response?.data || t('roomConfig.loadFailed'), { position: 'bottom-center' })
        if (isMounted) {
          onOpenChange(false)
        }
      } finally {
        if (isMounted) {
          setIsLoading(false)
        }
      }
    }

    loadRoomConfig()

    return () => {
      isMounted = false
    }
  }, [open, onOpenChange, roomInfo.room_id, t])

  const handleConfigToggle = (key: 'auto_record' | 'notify', checked: boolean) => {
    setRoomConfig((current) => {
      if (!current) {
        return {
          room_id: roomInfo.room_id,
          auto_record: key === 'auto_record' ? checked : false,
          notify: key === 'notify' ? checked : false,
        }
      }

      return {
        ...current,
        [key]: checked,
      }
    })
  }

  const handleGroupInheritChange = (section: SettingsSectionDescriptor, inherit: boolean) => {
    setInheritByGroup((current) => ({ ...current, [section.id]: inherit }))
    if (inherit) {
      // Switching a group back to inherit clears its overrides so PUT omits them.
      setRoomConfig((current) => (current ? clearGroupOverrides(current, section) : current))
    }
  }

  const handleOverrideChange = (fieldPath: string, value: unknown) => {
    setRoomConfig((current) => (
      current ? setSettingByPath(current, fieldPath, value) : current
    ))
  }

  const handleSaveConfig = async () => {
    if (!roomConfig) {
      return
    }

    setIsSaving(true)
    try {
      const request: UpdateRoomConfigRequest = {
        auto_record: roomConfig.auto_record,
        notify: roomConfig.notify,
        record_duration_minutes: roomConfig.record_duration_minutes,
        qn: roomConfig.qn ?? 0,
        only_audio: roomConfig.only_audio ?? false,
        record_danmaku: roomConfig.record_danmaku ?? false,
        record_subtitle: roomConfig.record_subtitle ?? false,
        stream_profiles: roomConfig.stream_profiles ?? [],
        delete_oldest_on_low_disk: roomConfig.delete_oldest_on_low_disk ?? false,
        ...buildRoomOverridePayload(roomConfig, globalSettings, inheritByGroup),
      }
      const updatedConfig = await apiClient.updateRoomConfig(roomInfo.room_id, request)
      setRoomConfig(updatedConfig)
      setInheritByGroup(deriveInheritGroups(updatedConfig))
      void mutate('subscribe/auto-record-ids')
      onOpenChange(false)
      toast.success(t('roomConfig.updateSuccess'), { position: 'bottom-center' })
    } catch (error: any) {
      console.error('Failed to update room config:', error)
      toast.error(error.response?.data || t('roomConfig.updateFailed'), { position: 'bottom-center' })
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[min(90dvh,calc(100dvh-2rem))] flex-col gap-0 p-0 sm:max-w-2xl">
        <div className="shrink-0 px-6 pt-6 pb-2">
          <DialogHeader>
            <DialogTitle>{t('roomConfig.title')}</DialogTitle>
            <DialogDescription>
              {t('roomConfig.description', { name: roomInfo.uname ?? t('subscribeCard.roomFallback', { roomId: roomInfo.room_id }) })}
            </DialogDescription>
          </DialogHeader>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
          <div className="space-y-4 px-6 py-4">
            {isLoading || !roomConfig ? (
              <p className="text-sm text-muted-foreground">{t('roomConfig.loading')}</p>
            ) : (
              <>
                <div className="flex items-center justify-between gap-4 rounded-lg border border-border bg-muted/20 p-3">
                  <div className="space-y-1">
                    <Label htmlFor={`auto-record-${roomInfo.room_id}`}>{t('roomConfig.autoRecord')}</Label>
                    <p className="text-sm text-muted-foreground">
                      {t('roomConfig.autoRecordHint')}
                    </p>
                  </div>
                  <Switch
                    id={`auto-record-${roomInfo.room_id}`}
                    checked={roomConfig.auto_record}
                    onCheckedChange={(checked) => handleConfigToggle('auto_record', checked)}
                    disabled={isSaving}
                  />
                </div>

                <div className="flex items-center justify-between gap-4 rounded-lg border border-border bg-muted/20 p-3">
                  <div className="space-y-1">
                    <Label htmlFor={`notify-${roomInfo.room_id}`}>{t('roomConfig.notify')}</Label>
                    <p className="text-sm text-muted-foreground">
                      {t('roomConfig.notifyHint')}
                    </p>
                  </div>
                  <Switch
                    id={`notify-${roomInfo.room_id}`}
                    checked={roomConfig.notify}
                    onCheckedChange={(checked) => handleConfigToggle('notify', checked)}
                    disabled={isSaving}
                  />
                </div>

                <RecordStartOptions
                  view="subscribe"
                  value={{
                    record_duration_minutes: roomConfig.record_duration_minutes,
                    qn: roomConfig.qn,
                    only_audio: roomConfig.only_audio,
                    record_danmaku: roomConfig.record_danmaku,
                    record_subtitle: roomConfig.record_subtitle,
                    stream_profiles: roomConfig.stream_profiles,
                    delete_oldest_on_low_disk: roomConfig.delete_oldest_on_low_disk,
                  }}
                  onChange={(opts) => setRoomConfig((current) => (current ? { ...current, ...opts } : current))}
                  disabled={isSaving}
                  durationHintKey="roomConfig.recordDurationHint"
                />

                {SETTINGS_SECTIONS.map((section) => {
                  const inherit = inheritByGroup[section.id]
                  return (
                    <div
                      key={section.id}
                      className="space-y-3 rounded-lg border border-border bg-muted/20 p-3"
                    >
                      <div className="flex items-start justify-between gap-4">
                        <div className="min-w-0 space-y-1">
                          <p className="text-sm font-semibold">{t(section.titleKey)}</p>
                          <p className="text-sm text-muted-foreground">
                            {t('roomConfig.inheritGlobalHint')}
                          </p>
                        </div>
                        <div className="flex shrink-0 items-center gap-2">
                          <Label
                            htmlFor={`inherit-global-${section.id}-${roomInfo.room_id}`}
                            className="text-sm font-normal text-muted-foreground"
                          >
                            {t('roomConfig.inheritGlobal')}
                          </Label>
                          <Switch
                            id={`inherit-global-${section.id}-${roomInfo.room_id}`}
                            checked={inherit}
                            onCheckedChange={(checked) => handleGroupInheritChange(section, checked)}
                            disabled={isSaving}
                          />
                        </div>
                      </div>
                      <div className="grid gap-3 sm:grid-cols-2">
                        {section.fields.map((field) => (
                          <SettingsFieldControl
                            key={field.path}
                            id={`room-${section.id}-${field.path.split('.').join('-')}`}
                            field={field}
                            value={getSettingByPath(roomConfig, field.path)}
                            fallbackValue={getSettingByPath(globalSettings, field.path)}
                            inherit={inherit}
                            disabled={isSaving || inherit}
                            className={field.fullWidth ? 'sm:col-span-2' : undefined}
                            onChange={(value) => handleOverrideChange(field.path, value)}
                          />
                        ))}
                      </div>
                    </div>
                  )
                })}
              </>
            )}
          </div>
        </div>

        <div className="shrink-0 border-t border-border px-6 py-4">
          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => onOpenChange(false)}
              disabled={isLoading || isSaving}
            >
              {t('roomConfig.cancel')}
            </Button>
            <Button
              onClick={handleSaveConfig}
              disabled={isLoading || isSaving || !roomConfig}
            >
              {isSaving ? t('roomConfig.saving') : t('roomConfig.save')}
            </Button>
          </DialogFooter>
        </div>
      </DialogContent>
    </Dialog>
  )
}
