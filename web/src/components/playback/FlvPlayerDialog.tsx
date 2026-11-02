import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { apiClient } from '@/lib/api'

interface FlvPlayerDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Path relative to the recordings root (OUTPUT_DIR). */
  path: string
  name: string
}

/**
 * Minimal FLV player for the batch-repair list. Recorded files are FLV, which
 * browsers cannot play natively, so this dialog feeds the range-enabled
 * /files/playback stream through flv.js (MSE) instead of the mp4/m4a player.
 */
export function FlvPlayerDialog({ open, onOpenChange, path, name }: FlvPlayerDialogProps) {
  const { t } = useTranslation()
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const closeRef = useRef(onOpenChange)
  closeRef.current = onOpenChange

  useEffect(() => {
    const video = videoRef.current
    if (!open || !video) return

    let cancelled = false
    let dispose: (() => void) | undefined

    void (async () => {
      try {
        const { default: flvjs } = await import('flv.js')
        if (cancelled || !videoRef.current) return
        if (!flvjs.isSupported()) {
          toast.error(t('repairView.flvUnsupported'))
          closeRef.current(false)
          return
        }
        // Prefer a presigned URL (works without cookies / through CDNs);
        // fall back to the cookie-authenticated route on older backends.
        let url = apiClient.getPlaybackUrl(path)
        try {
          const minted = await apiClient.mintPlaybackUrl(path)
          if (minted?.url) url = minted.url
        } catch {
          // Keep the cookie URL.
        }
        if (cancelled || !videoRef.current) return
        const player = flvjs.createPlayer(
          {
            type: 'flv',
            url,
            isLive: false,
            hasAudio: true,
            hasVideo: true,
            cors: true,
            withCredentials: true,
          },
          { enableStashBuffer: false, stashInitialSize: 128 * 1024 },
        )
        player.on(flvjs.Events.ERROR, () => {
          toast.error(t('repairView.playFailed'))
        })
        player.attachMediaElement(videoRef.current)
        player.load()
        // Autoplay can be blocked; the native controls remain available.
        void player.play()
        dispose = () => {
          try {
            player.pause()
            player.unload()
            player.detachMediaElement()
            player.destroy()
          } catch {
            // The element may already be detached while the dialog closes.
          }
        }
      } catch (error) {
        console.error('Failed to start FLV playback:', error)
        if (!cancelled) {
          toast.error(t('repairView.playFailed'))
        }
      }
    })()

    return () => {
      cancelled = true
      dispose?.()
      const element = videoRef.current
      if (element) {
        element.removeAttribute('src')
        element.load()
      }
    }
  }, [open, path, t])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-5xl">
        <DialogHeader>
          <DialogTitle className="break-all">{name}</DialogTitle>
        </DialogHeader>
        <video
          ref={videoRef}
          controls
          autoPlay
          playsInline
          className="aspect-video w-full rounded-md bg-black"
        />
      </DialogContent>
    </Dialog>
  )
}
