import { useState } from 'react'
import { CopySimpleIcon, GearSixIcon, PlayIcon, TrashIcon } from '@phosphor-icons/react'
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
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { RoomConfigDialog } from '@/components/RoomConfigDialog'
import { useRole } from '@/lib/role-context'
import type { RoomInfo } from '@/lib/types'

interface SubscribeListRowProps {
  roomInfo: RoomInfo
  isRecording?: boolean
  autoRecord?: boolean
  addedAt?: number
  onUnsubscribe: (roomId: number) => Promise<void>
  onStartRecord: (roomId: number) => Promise<void>
}

export function SubscribeListRow({
  roomInfo,
  isRecording = false,
  autoRecord = false,
  addedAt,
  onUnsubscribe,
  onStartRecord,
}: SubscribeListRowProps) {
  const { t } = useTranslation()
  const { isReadOnly } = useRole()
  const [isConfigOpen, setIsConfigOpen] = useState(false)
  const [isUnsubscribeOpen, setIsUnsubscribeOpen] = useState(false)
  const [isBusy, setIsBusy] = useState(false)
  const isLive = roomInfo.live_status === 1

  const copyRoomId = async () => {
    try {
      await navigator.clipboard.writeText(String(roomInfo.room_id))
      toast.success(t('toast.copyRoomIdSuccess', { roomId: roomInfo.room_id }), { position: 'bottom-center' })
    } catch {
      toast.error(t('toast.copyRoomIdFailed'), { position: 'bottom-center' })
    }
  }

  const unsubscribe = async () => {
    setIsUnsubscribeOpen(false)
    setIsBusy(true)
    try {
      await onUnsubscribe(roomInfo.room_id)
    } finally {
      setIsBusy(false)
    }
  }

  return (
    <div className="flex min-h-[58px] items-center gap-3 border-b border-border/60 px-3 py-2 last:border-b-0 hover:bg-muted/30">
      <div className="relative flex size-9 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-secondary text-xs font-semibold text-secondary-foreground">
        {roomInfo.cover ? <img src={roomInfo.cover} alt="" className="size-full object-cover" loading="lazy" /> : roomInfo.uname?.slice(0, 1).toUpperCase()}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-sm font-semibold">{roomInfo.uname || t('subscribeCard.roomFallback', { roomId: roomInfo.room_id })}</span>
          <button type="button" className="shrink-0 rounded p-0.5 text-muted-foreground hover:text-foreground" onClick={() => void copyRoomId()} aria-label={t('subscribeCard.copyRoomIdAria', { roomId: roomInfo.room_id })}>
            <CopySimpleIcon size={12} />
          </button>
          <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{roomInfo.room_id}</span>
          <Badge variant={isLive ? 'default' : 'secondary'} className="h-5 text-[10px]">{isLive ? t('subscribeCard.live') : t('subscribeCard.offline')}</Badge>
          {isRecording && <Badge className="h-5 bg-accent text-[10px] text-accent-foreground">{t('subscribeCard.recording')}</Badge>}
          {autoRecord && <Badge variant="outline" className="h-5 border-teal-500/40 text-[10px] text-teal-700 dark:text-teal-300">{t('subscribeCard.autoRecord')}</Badge>}
        </div>
        <div className="mt-0.5 flex min-w-0 items-center gap-3 text-[11px] text-muted-foreground">
          <span className="truncate">{roomInfo.title || t('subscribeCard.loadingTitle')}</span>
          {typeof addedAt === 'number' && addedAt > 0 && <span className="hidden shrink-0 sm:inline">{new Date(addedAt * 1000).toLocaleString()}</span>}
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-0.5">
        {!isReadOnly && (
          <Button variant="ghost" size="icon" className="size-7" onClick={() => setIsConfigOpen(true)} aria-label={t('subscribeCard.roomConfig')} title={t('subscribeCard.roomConfig')}>
            <GearSixIcon size={14} />
          </Button>
        )}
        {!isReadOnly && !isRecording && (
          <Button variant="ghost" size="icon" className="size-7" disabled={isBusy} onClick={() => void onStartRecord(roomInfo.room_id)} aria-label={t('subscribeCard.startRecording')} title={t('subscribeCard.startRecording')}>
            <PlayIcon size={14} weight="fill" />
          </Button>
        )}
        {!isReadOnly && (
          <Button variant="destructive-ghost" size="icon" className="size-7" onClick={() => setIsUnsubscribeOpen(true)} aria-label={t('subscribeCard.unsubscribe')} title={t('subscribeCard.unsubscribe')}>
            <TrashIcon size={14} />
          </Button>
        )}
      </div>
      <RoomConfigDialog roomInfo={roomInfo} open={isConfigOpen} onOpenChange={setIsConfigOpen} />
      <AlertDialog open={isUnsubscribeOpen} onOpenChange={setIsUnsubscribeOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('subscribeCard.unsubTitle')}</AlertDialogTitle>
            <AlertDialogDescription>{t('subscribeCard.unsubDescription', { name: roomInfo.uname || roomInfo.room_id })}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isBusy}>{t('fileCard.cancel')}</AlertDialogCancel>
            <AlertDialogAction disabled={isBusy} onClick={(event) => { event.preventDefault(); void unsubscribe() }}>{t('subscribeCard.confirmCancel')}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
