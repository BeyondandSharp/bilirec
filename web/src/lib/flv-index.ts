import type { FlvKeyframeIndex } from "./types"

/**
 * Inject a server-built keyframe index into flv.js. flv.js only makes VOD
 * seeking available when onMetaData contains keyframes/filepositions; older
 * recordings (e.g. test_2.flv) do not, so the player gets the index separately
 * and it is installed into the private MediaInfo used by seek().
 */
export function injectFlvKeyframeIndex(player: unknown, index: FlvKeyframeIndex): boolean {
  const transmuxer = (player as { _transmuxer?: any })?._transmuxer
  const controller = transmuxer?._controller ?? transmuxer
  const mediaInfo = controller?._mediaInfo ?? (player as { _mediaInfo?: any })?._mediaInfo
  if (!mediaInfo) return false
  const keyframes = { times: index.times_ms, filepositions: index.filepositions }
  mediaInfo.hasKeyframesIndex = true
  mediaInfo.keyframesIndex = keyframes
  const segment = mediaInfo.segments?.[0]
  if (segment) {
    segment.hasKeyframesIndex = true
    segment.keyframesIndex = keyframes
  }
  return true
}
