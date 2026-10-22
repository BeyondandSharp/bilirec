import { useState } from 'react'
import { StopIcon, CopySimpleIcon, ClockIcon, VideoCameraIcon } from '@phosphor-icons/react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useRole } from '@/lib/role-context'
import { formatDuration, formatFileSize } from '@/lib/utils'
import type { RecordTask } from '@/lib/types'

interface RecordListRowProps {
  task: RecordTask
  onStop: (roomId: number) => void
}

export function RecordListRow({ task, onStop }: RecordListRowProps) {
  const { t } = useTranslation()
  const { isReadOnly } = useRole()
  const [isStopping, setIsStopping] = useState(false)
  const roomId = task.roomInfo?.room_id ?? task.roomId
  const name = task.roomInfo?.uname || t('recordCard.roomFallback', { roomId })
  const canStop = !isReadOnly && (task.status === 'recording' || task.status === 'recovering')
  const startedAt = (() => {
    if (typeof task.startTime === 'number' && task.startTime > 0) {
      return new Date(task.startTime > 1e12 ? task.startTime : task.startTime * 1000)
    }
    return null
  })()

  const copyRoomId = async () => {
    try {
      await navigator.clipboard.writeText(String(roomId))
      toast.success(t('toast.copyRoomIdSuccess', { roomId }), { position: 'bottom-center' })
    } catch {
      toast.error(t('toast.copyRoomIdFailed'), { position: 'bottom-center' })
    }
  }

  const stop = async () => {
    if (!canStop || isStopping) return
    setIsStopping(true)
    try {
      await Promise.resolve(onStop(task.roomId))
    } finally {
      setIsStopping(false)
    }
  }

  return (
    <div className="flex min-h-[58px] items-center gap-3 border-b border-border/60 px-3 py-2 last:border-b-0 hover:bg-muted/30">
      <div className="relative flex size-9 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-secondary text-xs font-semibold text-secondary-foreground">
        {task.roomInfo?.cover ? (
          <img src={task.roomInfo.cover} alt="" className="size-full object-cover" loading="lazy" />
        ) : (
          name.slice(0, 1).toUpperCase()
        )}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-sm font-semibold">{name}</span>
          <button
            type="button"
            className="shrink-0 rounded p-0.5 text-muted-foreground hover:text-foreground"
            onClick={() => void copyRoomId()}
            aria-label={t('recordCard.copyRoomIdAria', { roomId })}
            title={t('recordCard.copyRoomIdTitle', { roomId })}
          >
            <CopySimpleIcon size={12} />
          </button>
          <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{roomId}</span>
          {task.status === 'recording' && <Badge className="h-5 bg-accent text-[10px] text-accent-foreground">{t('recordCard.recording')}</Badge>}
          {task.status === 'recovering' && <Badge variant="outline" className="h-5 border-accent text-[10px] text-accent">{t('recordCard.recovering')}</Badge>}
          {task.isAudioOnly && <Badge variant="secondary" className="h-5 text-[10px]">{t('recordCard.audioOnlyBadge')}</Badge>}
        </div>
        <div className="mt-0.5 flex min-w-0 items-center gap-3 text-[11px] text-muted-foreground">
          <span className="truncate">{task.roomInfo?.title || task.sessionTitle || '—'}</span>
          {startedAt && <span className="hidden shrink-0 items-center gap-1 sm:flex"><ClockIcon size={11} />{startedAt.toLocaleString()}</span>}
        </div>
      </div>
      <div className="hidden shrink-0 items-center gap-3 font-mono text-[11px] text-muted-foreground sm:flex">
        {task.recordedTime !== undefined && <span className="flex items-center gap-1"><ClockIcon size={12} />{formatDuration(task.recordedTime)}</span>}
        {task.fileSize !== undefined && <span className="flex items-center gap-1"><VideoCameraIcon size={12} />{formatFileSize(task.fileSize)}</span>}
      </div>
      {canStop && (
        <Button
          variant="destructive-ghost"
          size="icon"
          className="size-8 shrink-0"
          onClick={() => void stop()}
          disabled={isStopping}
          aria-label={t('recordCard.stopRecording')}
          title={t('recordCard.stopRecording')}
        >
          <StopIcon size={15} weight="fill" />
        </Button>
      )}
    </div>
  )
}
