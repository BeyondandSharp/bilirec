import type { TFunction } from 'i18next'
import type { GlobalSettings, RoomConfig, UpdateRoomConfigRequest } from './types'

/**
 * Shared schema for the FEATURES-V2 settings fields
 * (docs/FEATURES-V2.md §1.1). Both the global `SettingsView` and the per-room
 * override UI in `RoomConfigDialog` render from these descriptors so labels,
 * hints, ranges and options stay in sync.
 */

export type SettingsSectionId = 'segment' | 'naming' | 'cover' | 'danmaku' | 'flv' | 'bili'

export type SettingsFieldKind = 'boolean' | 'number' | 'string' | 'textarea' | 'enum' | 'string-list'

export interface SettingsFieldOption {
  value: string
  /** i18n key, e.g. `settingsView.fields.segmentModeOff`. */
  labelKey: string
}

export interface SettingsFieldDescriptor {
  /** Dot path inside GlobalSettings / RoomConfig (e.g. `danmaku_types.raw_events`). */
  path: string
  kind: SettingsFieldKind
  /** i18n key for the field label. */
  labelKey: string
  /** i18n key for the helper text under/next to the label. */
  hintKey?: string
  /** i18n key for an explicit input placeholder (defaults to the global value). */
  placeholderKey?: string
  options?: SettingsFieldOption[]
  min?: number
  max?: number
  /** Render across both grid columns (textareas + boolean toggle rows). */
  fullWidth?: boolean
  /** Use a monospace font in the input/textarea. */
  monospace?: boolean
}

export interface SettingsSectionDescriptor {
  id: SettingsSectionId
  titleKey: string
  /** One-line group description shown under the title. */
  descriptionKey?: string
  fields: SettingsFieldDescriptor[]
}

/**
 * Built-in defaults from docs/FEATURES-V2.md §1.1. Only used as a fallback
 * (e.g. the backend predates the settings API) so placeholders still render;
 * the server always wins when `GET /settings` succeeds.
 */
export const FALLBACK_GLOBAL_SETTINGS: GlobalSettings = {
  segment_mode: 'off',
  segment_minutes: 120,
  segment_size_mb: 2048,
  segment_by_title: false,
  min_file_size_mb: 0,
  filename_template: '',
  title_filter_patterns: '',
  save_stream_cover: false,
  danmaku_types: {
    raw_events: false,
    super_chat: true,
    gift: true,
    guard: true,
  },
  danmaku_transport: 'auto',
  flv_write_metadata: true,
  flv_split_on_script_tag: false,
  flv_disable_split_on_annexb: true,
  bili_live_api_host: 'https://api.live.bilibili.com',
  bili_live_api_fallback_host: '',
  bili_cdn: [],
  bili_cdn_fallback: false,
  bili_force_source: false,
  bili_replace_cn01: [],
}

/** Clone a settings object so edit drafts never mutate fetched data. */
export function cloneSettings<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

export function getSettingByPath(source: unknown, path: string): unknown {
  let cursor: unknown = source
  for (const segment of path.split('.')) {
    if (cursor === null || cursor === undefined || typeof cursor !== 'object') {
      return undefined
    }
    cursor = (cursor as Record<string, unknown>)[segment]
  }
  return cursor
}

/** Immutably set a (possibly nested) dot path, cloning each level on the way. */
export function setSettingByPath<T extends object>(source: T, path: string, value: unknown): T {
  const segments = path.split('.')
  const clone: Record<string, unknown> = { ...(source as Record<string, unknown>) }
  let cursor = clone
  for (let i = 0; i < segments.length - 1; i += 1) {
    const key = segments[i]
    const child = cursor[key]
    const next: Record<string, unknown> =
      child !== null && typeof child === 'object' && !Array.isArray(child)
        ? { ...(child as Record<string, unknown>) }
        : {}
    cursor[key] = next
    cursor = next
  }
  cursor[segments[segments.length - 1]] = value
  return clone as T
}

/** Immutably remove a (possibly nested) dot path; empty parent objects are dropped. */
export function deleteSettingByPath<T extends object>(source: T, path: string): T {
  const segments = path.split('.')
  const key = segments[0]
  const clone: Record<string, unknown> = { ...(source as Record<string, unknown>) }
  if (segments.length === 1) {
    delete clone[key]
    return clone as T
  }
  const child = clone[key]
  if (child === null || typeof child !== 'object' || Array.isArray(child)) {
    return source
  }
  const nextChild = deleteSettingByPath(child as object, segments.slice(1).join('.'))
  if (Object.keys(nextChild).length === 0) {
    delete clone[key]
  } else {
    clone[key] = nextChild
  }
  return clone as T
}

/** True when a room config carries an explicit (non-null) override for `path`. */
export function hasSettingOverride(source: unknown, path: string): boolean {
  const value = getSettingByPath(source, path)
  return value !== undefined && value !== null
}

/** Human-readable rendering used for global-value placeholders/hints. */
export function formatSettingValue(
  field: SettingsFieldDescriptor,
  value: unknown,
  t: TFunction,
): string {
  if (value === undefined || value === null || value === '') {
    return '—'
  }
  switch (field.kind) {
    case 'boolean':
      return value === true
        ? t('settingsView.fields.enabled')
        : t('settingsView.fields.disabled')
    case 'enum': {
      const option = field.options?.find((item) => item.value === value)
      return option ? t(option.labelKey) : String(value)
    }
    case 'string-list':
      return Array.isArray(value) && value.length > 0 ? value.join(', ') : '—'
    default:
      return String(value)
  }
}

export const SETTINGS_SECTIONS: SettingsSectionDescriptor[] = [
  {
    id: 'segment',
    titleKey: 'settingsView.sections.segment',
    descriptionKey: 'settingsView.sections.segmentHint',
    fields: [
      {
        path: 'segment_mode',
        kind: 'enum',
        labelKey: 'settingsView.fields.segmentMode',
        hintKey: 'settingsView.fields.segmentModeHint',
        options: [
          { value: 'off', labelKey: 'settingsView.fields.segmentModeOff' },
          { value: 'time', labelKey: 'settingsView.fields.segmentModeTime' },
          { value: 'size', labelKey: 'settingsView.fields.segmentModeSize' },
        ],
      },
      {
        path: 'segment_by_title',
        kind: 'boolean',
        labelKey: 'settingsView.fields.segmentByTitle',
        hintKey: 'settingsView.fields.segmentByTitleHint',
        fullWidth: true,
      },
      {
        path: 'segment_minutes',
        kind: 'number',
        labelKey: 'settingsView.fields.segmentMinutes',
        hintKey: 'settingsView.fields.segmentMinutesHint',
        min: 1,
        max: 1440,
      },
      {
        path: 'segment_size_mb',
        kind: 'number',
        labelKey: 'settingsView.fields.segmentSizeMb',
        hintKey: 'settingsView.fields.segmentSizeMbHint',
        min: 16,
        max: 65536,
      },
    ],
  },
  {
    id: 'naming',
    titleKey: 'settingsView.sections.naming',
    descriptionKey: 'settingsView.sections.namingHint',
    fields: [
      {
        path: 'min_file_size_mb',
        kind: 'number',
        labelKey: 'settingsView.fields.minFileSizeMb',
        hintKey: 'settingsView.fields.minFileSizeMbHint',
        min: 0,
        max: 1024,
      },
      {
        // Relative path template under OUTPUT_DIR: '/' creates subdirectories
        // (FEATURES-V2 §3.2, amended 2026-10-20). Absolute paths, '..' segments
        // and backslashes are rejected by the server with a 400 message that
        // the settings page renders as-is.
        path: 'filename_template',
        kind: 'string',
        labelKey: 'settingsView.fields.filenameTemplate',
        hintKey: 'settingsView.fields.filenameTemplateHint',
        placeholderKey: 'settingsView.fields.filenameTemplatePlaceholder',
        fullWidth: true,
        monospace: true,
      },
      {
        path: 'title_filter_patterns',
        kind: 'textarea',
        labelKey: 'settingsView.fields.titleFilterPatterns',
        hintKey: 'settingsView.fields.titleFilterPatternsHint',
        placeholderKey: 'settingsView.fields.titleFilterPatternsPlaceholder',
        fullWidth: true,
        monospace: true,
      },
    ],
  },
  {
    id: 'cover',
    titleKey: 'settingsView.sections.cover',
    fields: [
      {
        path: 'save_stream_cover',
        kind: 'boolean',
        labelKey: 'settingsView.fields.saveStreamCover',
        hintKey: 'settingsView.fields.saveStreamCoverHint',
        fullWidth: true,
      },
    ],
  },
  {
    id: 'danmaku',
    titleKey: 'settingsView.sections.danmaku',
    descriptionKey: 'settingsView.sections.danmakuHint',
    fields: [
      {
        path: 'danmaku_types.raw_events',
        kind: 'boolean',
        labelKey: 'settingsView.fields.danmakuRawEvents',
        hintKey: 'settingsView.fields.danmakuRawEventsHint',
        fullWidth: true,
      },
      {
        path: 'danmaku_types.super_chat',
        kind: 'boolean',
        labelKey: 'settingsView.fields.danmakuSuperChat',
        fullWidth: true,
      },
      {
        path: 'danmaku_types.gift',
        kind: 'boolean',
        labelKey: 'settingsView.fields.danmakuGift',
        fullWidth: true,
      },
      {
        path: 'danmaku_types.guard',
        kind: 'boolean',
        labelKey: 'settingsView.fields.danmakuGuard',
        fullWidth: true,
      },
      {
        path: 'danmaku_transport',
        kind: 'enum',
        labelKey: 'settingsView.fields.danmakuTransport',
        hintKey: 'settingsView.fields.danmakuTransportHint',
        options: [
          { value: 'auto', labelKey: 'settingsView.fields.danmakuTransportAuto' },
          { value: 'ws', labelKey: 'settingsView.fields.danmakuTransportWs' },
          { value: 'wss', labelKey: 'settingsView.fields.danmakuTransportWss' },
        ],
      },
    ],
  },
  {
    id: 'flv',
    titleKey: 'settingsView.sections.flv',
    fields: [
      {
        path: 'flv_write_metadata',
        kind: 'boolean',
        labelKey: 'settingsView.fields.flvWriteMetadata',
        hintKey: 'settingsView.fields.flvWriteMetadataHint',
        fullWidth: true,
      },
      {
        path: 'flv_split_on_script_tag',
        kind: 'boolean',
        labelKey: 'settingsView.fields.flvSplitOnScriptTag',
        hintKey: 'settingsView.fields.flvSplitOnScriptTagHint',
        fullWidth: true,
      },
      {
        path: 'flv_disable_split_on_annexb',
        kind: 'boolean',
        labelKey: 'settingsView.fields.flvDisableSplitOnAnnexb',
        hintKey: 'settingsView.fields.flvDisableSplitOnAnnexbHint',
        fullWidth: true,
      },
    ],
  },
  {
    id: 'bili',
    titleKey: 'settingsView.sections.bili',
    descriptionKey: 'settingsView.sections.biliHint',
    fields: [
      {
        path: 'bili_live_api_host',
        kind: 'string',
        labelKey: 'settingsView.fields.biliLiveApiHost',
        hintKey: 'settingsView.fields.biliLiveApiHostHint',
        placeholderKey: 'settingsView.fields.biliLiveApiHostPlaceholder',
        fullWidth: true,
        monospace: true,
      },
      {
        path: 'bili_live_api_fallback_host',
        kind: 'string',
        labelKey: 'settingsView.fields.biliLiveApiFallbackHost',
        hintKey: 'settingsView.fields.biliLiveApiFallbackHostHint',
        placeholderKey: 'settingsView.fields.biliLiveApiFallbackHostPlaceholder',
        fullWidth: true,
        monospace: true,
      },
      {
        path: 'bili_cdn',
        kind: 'string-list',
        labelKey: 'settingsView.fields.biliCdn',
        hintKey: 'settingsView.fields.biliCdnHint',
        placeholderKey: 'settingsView.fields.biliCdnPlaceholder',
        fullWidth: true,
        monospace: true,
      },
      {
        path: 'bili_cdn_fallback',
        kind: 'boolean',
        labelKey: 'settingsView.fields.biliCdnFallback',
        hintKey: 'settingsView.fields.biliCdnFallbackHint',
        fullWidth: true,
      },
      {
        path: 'bili_force_source',
        kind: 'boolean',
        labelKey: 'settingsView.fields.biliForceSource',
        hintKey: 'settingsView.fields.biliForceSourceHint',
        fullWidth: true,
      },
      {
        path: 'bili_replace_cn01',
        kind: 'string-list',
        labelKey: 'settingsView.fields.biliReplaceCn01',
        hintKey: 'settingsView.fields.biliReplaceCn01Hint',
        placeholderKey: 'settingsView.fields.biliReplaceCn01Placeholder',
        fullWidth: true,
        monospace: true,
      },
    ],
  },
]

/** All field descriptors flattened (handy for override/derivation helpers). */
export const SETTINGS_FIELDS: SettingsFieldDescriptor[] = SETTINGS_SECTIONS.flatMap(
  (section) => section.fields,
)

/** A room group inherits global settings when none of its fields has an override. */
export function deriveInheritGroups(config: unknown): Record<SettingsSectionId, boolean> {
  const inherit = { segment: true, naming: true, cover: true, danmaku: true, flv: true, bili: true }
  for (const section of SETTINGS_SECTIONS) {
    inherit[section.id] = !section.fields.some((field) => hasSettingOverride(config, field.path))
  }
  return inherit
}

/** Remove every override belonging to one group (used when switching it to inherit). */
export function clearGroupOverrides<T extends object>(
  config: T,
  section: SettingsSectionDescriptor,
): T {
  return section.fields.reduce((current, field) => deleteSettingByPath(current, field.path), config)
}

/**
 * Build the room-config PUT payload for the custom (non-inherited) groups:
 * every custom group field is materialised from its override, falling back to
 * the effective global value. Inherited groups are omitted so the backend
 * stores nil (= inherit) and clears any previous override.
 */
export function buildRoomOverridePayload(
  config: RoomConfig,
  globalValues: GlobalSettings,
  inheritByGroup: Record<SettingsSectionId, boolean>,
): Partial<UpdateRoomConfigRequest> {
  let payload: Record<string, unknown> = {}
  for (const section of SETTINGS_SECTIONS) {
    if (inheritByGroup[section.id]) {
      continue
    }
    for (const field of section.fields) {
      const override = getSettingByPath(config, field.path)
      const value = override !== undefined && override !== null
        ? override
        : getSettingByPath(globalValues, field.path)
      payload = setSettingByPath(payload, field.path, value)
    }
  }
  return payload as Partial<UpdateRoomConfigRequest>
}
