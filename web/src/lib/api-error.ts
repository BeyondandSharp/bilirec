import { isAxiosError } from 'axios'

/** Extract a human-readable message from an API failure (server 400s carry plain Chinese text). */
export function getServerErrorMessage(error: unknown, fallback: string): string {
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
