import { useState } from 'react'
import {
  DownloadSimpleIcon,
  FileAudioIcon,
  FileTextIcon,
  FileVideoIcon,
  FolderIcon,
  PlayIcon,
} from '@phosphor-icons/react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { apiClient } from '@/lib/api'
import {
  formatFileSize,
  isConvertibleVideoFile,
  isDanmakuSidecarFile,
  isFLVFile,
  isRecordingMediaFilename,
} from '@/lib/utils'
import type { RecordFile } from '@/lib/types'

interface FileListRowProps {
  file: RecordFile
  currentPath?: string
  onNavigate?: (path: string) => void
  onPlayback: (path: string, name: string) => void
  repairSelected?: boolean
  onToggleRepairSelection?: (path: string) => void
}

export function FileListRow({
  file,
  currentPath = '',
  onNavigate,
  onPlayback,
  repairSelected = false,
  onToggleRepairSelection,
}: FileListRowProps) {
  const { t } = useTranslation()
  const [isDownloading, setIsDownloading] = useState(false)
  const isDir = file.is_dir
  const isDanmaku = isDanmakuSidecarFile(file.name)
  const isRecordingMedia = isRecordingMediaFilename(file.name)
  const isVideo = isConvertibleVideoFile(file.name)
  const extension = file.name.split('.').pop()?.toUpperCase() ?? ''
  const fullPath = currentPath ? `${currentPath}/${file.name}` : file.name
  const canRepairFile = !isDir && !file.is_recording && isFLVFile(file.name) && Boolean(onToggleRepairSelection)

  const download = async () => {
    if (isDir) {
      onNavigate?.(fullPath)
      return
    }
    setIsDownloading(true)
    try {
      await apiClient.downloadFile(fullPath, { suggestedName: file.name })
      toast.info(t('fileCard.downloading'))
    } catch (error) {
      console.error('Download failed:', error)
      toast.error(t('fileCard.downloadFailed'))
    } finally {
      setIsDownloading(false)
    }
  }

  return (
    <div
      className="flex min-h-[42px] cursor-pointer items-center gap-2.5 border-b border-border/60 px-3 py-1.5 last:border-b-0 hover:bg-muted/30"
      onClick={() => (isDir ? onNavigate?.(fullPath) : isVideo ? onPlayback(fullPath, file.name) : undefined)}
    >
      {canRepairFile && (
        <span onClick={(event) => event.stopPropagation()} className="flex shrink-0 items-center">
          <Checkbox
            checked={repairSelected}
            onCheckedChange={() => onToggleRepairSelection?.(fullPath)}
            aria-label={t('fileCard.selectRepair')}
            title={t('fileCard.selectRepair')}
          />
        </span>
      )}
      <div className={`flex size-7 shrink-0 items-center justify-center rounded-md ${isDir ? 'bg-amber-500/10 text-amber-700 dark:text-amber-300' : 'bg-primary/10 text-primary'}`}>
        {isDir ? <FolderIcon size={16} /> : isDanmaku ? <FileTextIcon size={16} /> : file.name.endsWith('.m4a') ? <FileAudioIcon size={16} /> : <FileVideoIcon size={16} />}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-xs font-semibold" title={file.name}>{file.name}</span>
          {isRecordingMedia && <Badge variant="outline" className="h-4 px-1 text-[9px]">{extension}</Badge>}
          {isDanmaku && <Badge variant="secondary" className="h-4 px-1 text-[9px]">弹幕</Badge>}
        </div>
      </div>
      <div className="hidden shrink-0 items-center gap-3 font-mono text-[10px] text-muted-foreground sm:flex">
        <span>{isDir ? '—' : formatFileSize(file.size)}</span>
        <span>{typeof file.mtime === 'number' && file.mtime > 0 ? new Date(file.mtime).toLocaleString() : '—'}</span>
      </div>
      <div className="flex shrink-0 items-center gap-0.5" onClick={(event) => event.stopPropagation()}>
        {isVideo && !isDir && (
          <Button variant="ghost" size="icon" className="size-7" onClick={() => onPlayback(fullPath, file.name)} aria-label={t('fileCard.playback')} title={t('fileCard.playback')}>
            <PlayIcon size={14} weight="fill" />
          </Button>
        )}
        {!isDir && (
          <Button variant="ghost" size="icon" className="size-7" onClick={() => void download()} disabled={isDownloading} aria-label={t('fileCard.download')} title={t('fileCard.download')}>
            <DownloadSimpleIcon size={14} />
          </Button>
        )}
      </div>
    </div>
  )
}
