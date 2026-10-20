const QN_LABEL_KEYS: Record<number, string> = {
  80: 'qualitySmooth',
  150: 'qualityHigh',
  250: 'qualitySuper',
  400: 'qualityBluRay',
  10000: 'qualityOriginal',
  20000: 'quality4k',
  30000: 'qualityDolby',
}

const STREAM_FORMAT_LABELS: Record<string, string> = {
  flv: 'FLV',
  fmp4: 'FMP4',
  ts: 'TS',
}

/**
 * Labels for the upstream source format, before an external downloader remuxes
 * it to FLV. ts/fmp4 sources are always HLS, so surface that in the label.
 */
const SOURCE_FORMAT_LABELS: Record<string, string> = {
  flv: 'FLV',
  ts: 'HLS-TS',
  fmp4: 'HLS-fMP4',
  'hls-ts': 'HLS-TS',
  'hls-fmp4': 'HLS-fMP4',
  'http-flv': 'FLV',
}

export function getRecordQualityLabelKey(qn: number | undefined): string | undefined {
  if (qn === undefined || qn <= 0) {
    return undefined
  }
  return QN_LABEL_KEYS[qn]
}

export function getRecordStreamFormatLabel(
  format: string | undefined,
): string | undefined {
  if (!format) {
    return undefined
  }
  return STREAM_FORMAT_LABELS[format]
}

/** Label for the original upstream format shown when it differs from the output format. */
export function getRecordSourceFormatLabel(
  format: string | undefined,
): string | undefined {
  if (!format) {
    return undefined
  }
  const normalized = format.trim().toLowerCase()
  if (!normalized) {
    return undefined
  }
  return SOURCE_FORMAT_LABELS[normalized] ?? normalized.toUpperCase()
}
