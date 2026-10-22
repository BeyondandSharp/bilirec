import { useState } from 'react'
import { PowerIcon } from '@phosphor-icons/react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
import { apiClient } from '@/lib/api'
import { getServerErrorMessage } from '@/lib/api-error'

interface RestartDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

function sleep(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms))
}

/** Admin-only graceful backend restart with a version probe before reload. */
export function RestartDialog({ open, onOpenChange }: RestartDialogProps) {
  const { t } = useTranslation()
  const [isRestarting, setIsRestarting] = useState(false)

  const handleRestart = async () => {
    if (isRestarting) {
      return
    }
    setIsRestarting(true)
    try {
      await apiClient.restartServer()
      toast.info(t('restart.restarting'), { position: 'bottom-center' })

      const deadline = Date.now() + 90_000
      while (Date.now() < deadline) {
        await sleep(1_000)
        try {
          await apiClient.getVersion()
          window.location.reload()
          return
        } catch {
          // The listener is still down/reloading; retry until the deadline.
        }
      }
      toast.error(t('restart.timeout'), { position: 'bottom-center' })
      setIsRestarting(false)
    } catch (error) {
      console.error('Failed to restart server:', error)
      toast.error(getServerErrorMessage(error, t('restart.failed')), {
        position: 'bottom-center',
      })
      setIsRestarting(false)
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={(next) => !isRestarting && onOpenChange(next)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle className="flex items-center gap-2">
            <PowerIcon size={18} className="text-destructive" />
            {t('restart.title')}
          </AlertDialogTitle>
          <AlertDialogDescription className="space-y-2">
            <span className="block">{t('restart.description')}</span>
            <span className="block text-xs text-muted-foreground">{t('restart.warning')}</span>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={isRestarting}>{t('restart.cancel')}</AlertDialogCancel>
          <AlertDialogAction
            disabled={isRestarting}
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            onClick={(event) => {
              event.preventDefault()
              void handleRestart()
            }}
          >
            {isRestarting ? t('restart.restartingShort') : t('restart.confirm')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
