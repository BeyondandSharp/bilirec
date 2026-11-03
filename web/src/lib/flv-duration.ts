/**
 * Probe a recorded FLV's duration from its last tag footer.
 *
 * FLV has no seek index at the end, but every tag is followed by a 4-byte
 * previous-tag-size field. Reading the last 4 bytes gives the last tag's full
 * size, which locates its 11-byte header and timestamp. This needs three tiny
 * HTTP Range requests instead of downloading/scanning a multi-GB file.
 *
 * Returns duration in milliseconds, or null when the footer is not usable
 * (trailing junk, truncated file, server without Range support, etc.).
 */
const cache = new Map<string, number>()

async function fetchRange(url: string, start: number, end: number, signal: AbortSignal): Promise<Uint8Array | null> {
  const resp = await fetch(url, {
    headers: { Range: `bytes=${start}-${end}` },
    credentials: 'include',
    signal,
  })
  if (resp.status !== 206) {
    // Do not read/download a 200 response body for a multi-GB file.
    void resp.body?.cancel()
    return null
  }
  return new Uint8Array(await resp.arrayBuffer())
}

export async function probeFlvDurationMs(url: string, cacheKey?: string): Promise<number | null> {
  const key = cacheKey || url
  const cached = cache.get(key)
  if (cached !== undefined) return cached

  const controller = new AbortController()
  const timer = window.setTimeout(() => controller.abort(), 5000)
  try {
    const head = await fetch(url, {
      headers: { Range: 'bytes=0-0' },
      credentials: 'include',
      signal: controller.signal,
    })
    if (head.status !== 206) {
      void head.body?.cancel()
      return null
    }
    const contentRange = head.headers.get('Content-Range') || ''
    void head.body?.cancel()
    const total = Number(contentRange.split('/').pop())
    if (!Number.isFinite(total) || total <= 17) return null

    const footer = await fetchRange(url, total - 4, total - 1, controller.signal)
    if (!footer || footer.length !== 4) return null
    const prevTagSize = new DataView(footer.buffer, footer.byteOffset, footer.byteLength).getUint32(0)
    if (prevTagSize < 11 || prevTagSize > total - 4 - 13) return null

    const tagStart = total - 4 - prevTagSize
    const header = await fetchRange(url, tagStart, tagStart + 10, controller.signal)
    if (!header || header.length !== 11) return null
    const type = header[0]
    if (type !== 8 && type !== 9 && type !== 18) return null
    const dataSize = (header[1] << 16) | (header[2] << 8) | header[3]
    if (tagStart + 11 + dataSize + 4 !== total) return null
    const timestamp = ((header[4] << 16) | (header[5] << 8) | header[6] | (header[7] << 24)) >>> 0
    if (timestamp <= 0) return null

    cache.set(key, timestamp)
    return timestamp
  } catch {
    return null
  } finally {
    window.clearTimeout(timer)
  }
}
