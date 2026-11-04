import { useState, useEffect, useMemo, useRef, useCallback } from 'react'
import { usePersistedState } from '@/hooks/use-persisted-state'
import { ViewModeToggle, type ViewMode } from '@/components/view/ViewModeToggle'
import { SortControl, type SortDirection } from '@/components/view/SortControl'
import { useInfiniteQuery, useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query'
import { useGridVirtualizer } from '@/hooks/use-grid-virtualizer'
import { FileCard } from './FileCard'
import { FileListRow } from './list/FileListRow'
import { EmptyState } from './EmptyState'
import { PlaybackPlayerDialog } from '@/components/playback/PlaybackPlayerDialog'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import { ArrowCounterClockwiseIcon, CaretLeftIcon, CheckSquareIcon, TrashSimpleIcon, WrenchIcon } from '@phosphor-icons/react'
import { RecordingBrowseTimeRange } from '@/components/files/RecordingBrowseTimeRange'
import { SearchBar } from './SearchBar'
import { apiClient } from '@/lib/api'
import { isNetworkError, markOnline, noteTransportFailure } from '@/lib/network-status'
import { toast } from 'sonner'
import type { RecordFile, RecordFileListResponse } from '@/lib/types'
import { useTranslation } from 'react-i18next'
import { loadOnlyMediaFiles, saveOnlyMediaFiles } from '@/lib/files-view-settings'
import { isFLVFile } from '@/lib/utils'
import { useRole } from '@/lib/role-context'
import {
  isRecordingBrowseRangeInvalid,
  recordingBrowseStampFromDatetimeLocal,
} from '@/lib/recording-browse-time'

type FilesLayoutConfig = {
  pageSize: number
}

const getFilesLayoutConfig = (width: number): FilesLayoutConfig => {
  if (width < 768) {
    return {
      pageSize: 14,
    }
  }

  if (width < 1280) {
    return {
      pageSize: 36,
    }
  }

  return {
    pageSize: 63,
  }
}

function FileCardSkeleton() {
  return (
    <div className="rounded-xl border border-border bg-card p-4 h-full w-full">
      <div className="flex h-full gap-3">
        <Skeleton className="h-12 w-12 rounded-lg shrink-0" />
        <div className="flex grow min-w-0 flex-col">
          <div className="mb-2">
            <Skeleton className="h-6 w-2/3" />
          </div>

          <div className="grow" />

          <div className="mb-2">
            <Skeleton className="h-4 w-1/3" />
          </div>

          <div className="flex gap-2">
            <Skeleton className="h-9 w-full rounded-md" />
          </div>
        </div>
      </div>
    </div>
  )
}

const getFilesQueryKey = (
  path: string,
  search: string,
  pageSize: number,
  onlyMedia: boolean,
  sort: 'name' | 'mtime',
  order: SortDirection,
  startStamp: string | null,
  endStamp: string | null,
) =>
  ['files', path, search, pageSize, onlyMedia, sort, order, startStamp ?? '', endStamp ?? ''] as const

const normalizeList = (items?: RecordFile[]) => {
  return Array.isArray(items)
    ? items.filter((file) => !file.is_dir || file.name !== '..')
    : []
}

function basenamesFromPaths(paths: readonly string[]): Set<string> {
  return new Set(
    paths.map((p) => {
      const normalized = p.replace(/\\/g, '/')
      const slash = normalized.lastIndexOf('/')
      return slash >= 0 ? normalized.slice(slash + 1) : normalized
    }),
  )
}

function removeDeletedFromFilesQueries(
  queryClient: QueryClient,
  browsePath: string,
  deletedBasenames: Set<string>,
) {
  if (deletedBasenames.size === 0) return

  queryClient.setQueriesData<InfiniteData<RecordFileListResponse>>(
    { queryKey: ['files', browsePath], exact: false },
    (old) => {
      if (!old?.pages?.length) return old

      let removed = 0
      const pages = old.pages.map((page) => {
        const items = page.items.filter((item) => {
          if (deletedBasenames.has(item.name)) {
            removed += 1
            return false
          }
          return true
        })
        return { ...page, items }
      })

      if (removed === 0) return old

      const first = pages[0]
      if (first) {
        pages[0] = { ...first, total: Math.max(0, first.total - removed) }
      }

      return { ...old, pages }
    },
  )
}

const CARD_MIN_WIDTH = 300
const GRID_GAP = 16
const CONTENT_PADDING_X = 32
const ESTIMATED_ROW_HEIGHT = 140

type PlaybackFile = {
  path: string
  name: string
}

export function FilesView() {
  const { t } = useTranslation()
  const [currentPath, setCurrentPath] = useState('')
  const [searchInput, setSearchInput] = useState('')
  const [search, setSearch] = useState('')
  const [onlyMedia, setOnlyMedia] = useState(() => loadOnlyMediaFiles())
  const [rangeStartInput, setRangeStartInput] = useState('')
  const [rangeEndInput, setRangeEndInput] = useState('')
  const [rangeStart, setRangeStart] = useState('')
  const [rangeEnd, setRangeEnd] = useState('')
  const [displayFiles, setDisplayFiles] = useState<RecordFile[]>([])
  const [displayTotal, setDisplayTotal] = useState(0)
  const [isSearchTransition, setIsSearchTransition] = useState(false)
  const [layoutConfig, setLayoutConfig] = useState<FilesLayoutConfig>(() =>
    getFilesLayoutConfig(window.innerWidth)
  )
  const [viewMode, setViewMode] = usePersistedState<ViewMode>('files-layout', 'grid')
  const [sortField, setSortField] = usePersistedState<'name' | 'mtime'>('files-sort', 'mtime')
  const [sortDirection, setSortDirection] = usePersistedState<SortDirection>('files-sort-dir', 'desc')
  const [playbackFile, setPlaybackFile] = useState<PlaybackFile | null>(null)
  const [selection, setSelection] = useState<Set<string>>(() => new Set())
  const [selectionMode, setSelectionMode] = useState(false)
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false)
  const [isBatchDeleting, setIsBatchDeleting] = useState(false)
  const { isReadOnly } = useRole()
  const scrollContainerRef = useRef<HTMLDivElement | null>(null)
  const loadMoreTriggerRef = useRef<HTMLDivElement | null>(null)
  const prevPathRef = useRef(currentPath)
  const prevSearchRef = useRef(search)
  const prevOnlyMediaRef = useRef(onlyMedia)
  const prevRangeStartRef = useRef(rangeStart)
  const prevRangeEndRef = useRef(rangeEnd)
  const lastAppliedListFilterRef = useRef(`${search}:${onlyMedia}::`)
  const queryClient = useQueryClient()

  const startStamp = useMemo(
    () => recordingBrowseStampFromDatetimeLocal(rangeStart, 'start'),
    [rangeStart],
  )
  const endStamp = useMemo(
    () => recordingBrowseStampFromDatetimeLocal(rangeEnd, 'end'),
    [rangeEnd],
  )
  const rangeInvalid = useMemo(
    () => isRecordingBrowseRangeInvalid(rangeStart, rangeEnd),
    [rangeStart, rangeEnd],
  )

  const clearRangeFilter = useCallback(() => {
    setRangeStartInput('')
    setRangeEndInput('')
    setRangeStart('')
    setRangeEnd('')
  }, [])

  useEffect(() => {
    const onResize = () => {
      const next = getFilesLayoutConfig(window.innerWidth)
      setLayoutConfig((prev) => (
        prev.pageSize === next.pageSize
      )
        ? prev
        : next)
    }

    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  const { pageSize } = layoutConfig

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setSearch(searchInput.trim())
    }, 250)
    return () => window.clearTimeout(timer)
  }, [searchInput])

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setRangeStart(rangeStartInput.trim())
      setRangeEnd(rangeEndInput.trim())
    }, 250)
    return () => window.clearTimeout(timer)
  }, [rangeStartInput, rangeEndInput])

  const queryKey = useMemo(
    () =>
      getFilesQueryKey(
        currentPath,
        search,
        pageSize,
        onlyMedia,
        sortField,
        sortDirection,
        startStamp,
        endStamp,
      ),
    [currentPath, search, pageSize, onlyMedia, sortField, sortDirection, startStamp, endStamp]
  )

  const {
    data,
    isPending,
    isFetching,
    isFetchingNextPage,
    isSuccess,
    isError,
    error,
    fetchNextPage: fetchNextPageQuery,
    hasNextPage
  } = useInfiniteQuery({
    queryKey,
    enabled: !rangeInvalid,
    queryFn: async ({ pageParam = 0 }: { pageParam: number }) => {
      const data = await apiClient.getFiles(
        currentPath,
        pageParam,
        pageSize,
        search,
        onlyMedia,
        sortField,
        sortDirection,
        startStamp ?? undefined,
        endStamp ?? undefined,
      )
      return {
        ...data,
        items: normalizeList(data.items)
      }
    },
    initialPageParam: 0,
    getNextPageParam: (lastPage: RecordFileListResponse, pages: RecordFileListResponse[]) => {
      const loaded = pages.reduce((sum, page) => sum + page.items.length, 0)
      return loaded < lastPage.total ? loaded : undefined
    },
    staleTime: 15000,
    refetchOnMount: 'always',
    refetchOnWindowFocus: 'always'
  })

  useEffect(() => {
    void queryClient.invalidateQueries({
      queryKey: ['files', currentPath],
      exact: false,
      refetchType: 'active'
    })
  }, [currentPath, queryClient])

  const { rows, fixedColumnsStyle, rowVirtualizer } = useGridVirtualizer<RecordFile | null>({
    scrollContainerRef,
    items: displayFiles,
    cardMinWidth: CARD_MIN_WIDTH,
    gridGap: GRID_GAP,
    contentPaddingX: CONTENT_PADDING_X,
    estimatedRowHeight: ESTIMATED_ROW_HEIGHT,
  })

  const queryFiles = useMemo(() => {
    if (!data?.pages?.length) return []
    return data.pages.flatMap((page: RecordFileListResponse) => page.items)
  }, [data])

  const queryTotal = useMemo(() => {
    if (!data?.pages?.length) return 0
    return data.pages[0]?.total ?? 0
  }, [data])

  const siblingFileNames = useMemo(
    () => new Set(queryFiles.map((file) => file.name)),
    [queryFiles]
  )

  const isInitialLoading = (isPending || isFetching) && displayFiles.length === 0
  const isLoadingMore = isFetchingNextPage
  const isDebouncingSearch = searchInput.trim() !== search
  const isSearching = isDebouncingSearch || isSearchTransition || (isFetching && search.length > 0)

  useEffect(() => {
    const isListFilterChange =
      prevPathRef.current === currentPath &&
      (prevSearchRef.current !== search ||
        prevOnlyMediaRef.current !== onlyMedia ||
        prevRangeStartRef.current !== rangeStart ||
        prevRangeEndRef.current !== rangeEnd)

    if (prevPathRef.current !== currentPath) {
      // Path changed: clear immediately so skeleton can represent new directory load.
      setDisplayFiles([])
      setDisplayTotal(0)
      setIsSearchTransition(false)
      setSelection(new Set())
      clearRangeFilter()
    } else if (isListFilterChange && displayFiles.length > 0) {
      // Search or media filter changed under same path: keep old items until new result arrives.
      setIsSearchTransition(true)
    }

    prevPathRef.current = currentPath
    prevSearchRef.current = search
    prevOnlyMediaRef.current = onlyMedia
    prevRangeStartRef.current = rangeStart
    prevRangeEndRef.current = rangeEnd
  }, [currentPath, search, onlyMedia, rangeStart, rangeEnd, pageSize, displayFiles.length, clearRangeFilter])

  useEffect(() => {
    if (!isSuccess || rangeInvalid) return

    const listFilterKey = `${search}:${onlyMedia}:${startStamp ?? ''}:${endStamp ?? ''}`
    if (lastAppliedListFilterRef.current !== listFilterKey) {
      scrollContainerRef.current?.scrollTo({ top: 0, behavior: 'smooth' })
      lastAppliedListFilterRef.current = listFilterKey
    }

    setDisplayFiles(queryFiles)
    setDisplayTotal(queryTotal)
    setIsSearchTransition(false)
  }, [isSuccess, queryFiles, queryTotal, search, onlyMedia, startStamp, endStamp, rangeInvalid])

  useEffect(() => {
    if (isSearchTransition && !isFetching && !isDebouncingSearch) {
      setIsSearchTransition(false)
    }
  }, [isSearchTransition, isFetching, isDebouncingSearch])

  useEffect(() => {
    if (isSuccess) {
      markOnline()
    }
  }, [isSuccess])

  useEffect(() => {
    if (!isError) return
    if (isNetworkError(error)) {
      noteTransportFailure(error)
      return
    }
    const err = error as { response?: { data?: string } }
    toast.error(t('filesView.loadError') + (err?.response?.data ? `: ${err.response.data}` : ''))
  }, [isError, error, t])

  const handleFetchNextPage = useCallback(async () => {
    if (isInitialLoading || isLoadingMore || isSearchTransition || !hasNextPage) return
    await fetchNextPageQuery()
  }, [isInitialLoading, isLoadingMore, isSearchTransition, hasNextPage, fetchNextPageQuery])

  useEffect(() => {
    if (!hasNextPage) return

    const root = scrollContainerRef.current
    const target = loadMoreTriggerRef.current
    if (!root || !target) return

    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0]
        if (entry?.isIntersecting) {
          void handleFetchNextPage()
        }
      },
      {
        root,
        rootMargin: '0px',
        threshold: 0.01
      }
    )

    observer.observe(target)
    return () => observer.disconnect()
  }, [handleFetchNextPage, hasNextPage, displayFiles.length, pageSize])

  const handleNavigate = (path: string) => {
    setSearchInput('')
    setSearch('')
    clearRangeFilter()
    setCurrentPath(path)
  }

  const handlePlayback = useCallback((path: string, name: string) => {
    setPlaybackFile({ path, name })
  }, [])

  const toggleSelection = useCallback((path: string) => {
    setSelection((current) => {
      const next = new Set(current)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })
  }, [])

  const addSelectedToRepair = async () => {
    if (selectedFlvPaths.length === 0) return
    try {
      await apiClient.addRepairTasks(selectedFlvPaths)
      toast.success(t('filesView.addToRepairSuccess'))
      setSelection(new Set())
    } catch (error: any) {
      toast.error(t('filesView.addToRepairFailed') + (error?.response?.data ? `: ${error.response.data}` : ''))
    }
  }

  const pathOfFile = (file: RecordFile) =>
    typeof file.path === 'string' && file.path.trim()
      ? file.path.replace(/\\/g, '/')
      : currentPath
        ? `${currentPath}/${file.name}`
        : file.name

  const selectableFiles = displayFiles.filter((file) => !file.is_dir && !file.is_recording)
  const selectedFiles = displayFiles.filter((file) => selection.has(pathOfFile(file)))
  const selectedFlvPaths = selectedFiles.filter((file) => isFLVFile(file.name)).map(pathOfFile)
  const allSelected =
    selectableFiles.length > 0 && selectableFiles.every((file) => selection.has(pathOfFile(file)))

  const toggleSelectionMode = () => {
    setSelection(new Set())
    setSelectionMode((current) => !current)
  }

  const toggleSelectAll = () => {
    setSelection(
      allSelected ? new Set() : new Set(selectableFiles.map(pathOfFile)),
    )
  }

  const invertSelection = () => {
    setSelection(
      new Set(
        selectableFiles
          .map(pathOfFile)
          .filter((path) => !selection.has(path)),
      ),
    )
  }

  const confirmBatchDelete = async () => {
    if (selectedFiles.length === 0 || isBatchDeleting) return
    setIsBatchDeleting(true)
    try {
      const paths = selectedFiles.map(pathOfFile)
      await apiClient.deleteFiles(paths)
      handleFileDeleted({ paths })
      setSelection(new Set())
      setDeleteDialogOpen(false)
      toast.success(t('filesView.deleteSuccess'))
    } catch (error: any) {
      toast.error(t('filesView.deleteFailed') + (error?.response?.data ? `: ${error.response.data}` : ''))
    } finally {
      setIsBatchDeleting(false)
    }
  }

  const handleFileDeleted = useCallback(
    ({ paths }: { paths: string[] }) => {
      const deletedNames = basenamesFromPaths(paths)
      setPlaybackFile((open) => {
        if (!open) return open
        const openBase = open.path.replace(/\\/g, '/').split('/').pop()
        if (openBase && deletedNames.has(openBase)) return null
        return open
      })
      removeDeletedFromFilesQueries(queryClient, currentPath, deletedNames)
      setDisplayFiles((prev) => {
        const next = prev.filter((file) => !deletedNames.has(file.name))
        const removedCount = prev.length - next.length
        if (removedCount > 0) {
          setDisplayTotal((total) => Math.max(0, total - removedCount))
        }
        return next
      })
      void queryClient.invalidateQueries({ queryKey: ['files', currentPath], exact: false })
    },
    [currentPath, queryClient],
  )

  const handleBack = () => {
    const parts = currentPath.split('/').filter(Boolean)
    parts.pop()
    setSearchInput('')
    setSearch('')
    clearRangeFilter()
    setCurrentPath(parts.join('/'))
  }

  return (
    <div className="flex flex-col h-full">
      <div className="relative z-10 border-b border-border bg-background p-3 sm:sticky sm:top-0 sm:p-4">
        <div className="flex items-center gap-2">
          <div className="flex min-w-0 flex-1 items-center gap-1.5 sm:gap-2">
            {currentPath && (
              <Button
                variant="ghost"
                size="icon"
                className="size-8 shrink-0 sm:size-9"
                onClick={handleBack}
                aria-label={t('filesView.back')}
                title={t('filesView.back')}
              >
                <CaretLeftIcon size={20} />
              </Button>
            )}
            <h2 className="truncate text-lg font-bold sm:text-xl">{t('filesView.title')}</h2>
          </div>

          <div className="flex shrink-0 items-center gap-1.5 sm:gap-2">
            {!isReadOnly && (
              <Button
                variant={selectionMode ? 'secondary' : 'outline'}
                size="icon"
                className="size-9 shrink-0"
                onClick={toggleSelectionMode}
                aria-label={selectionMode ? t('filesView.exitMultiSelect') : t('filesView.multiSelect')}
                title={selectionMode ? t('filesView.exitMultiSelect') : t('filesView.multiSelect')}
              >
                <CheckSquareIcon size={18} />
              </Button>
            )}
            <ViewModeToggle value={viewMode} onChange={setViewMode} />
            <SearchBar
              value={searchInput}
              onChange={setSearchInput}
              placeholder={t('filesView.searchPlaceholder')}
              isSearching={isSearching}
              searchingLabel={t('filesView.searching')}
              searchLabel={t('actions.search')}
              dialogTitle={t('filesView.searchPlaceholder')}
              inputWidth="w-[180px] sm:w-[240px]"
            />
          </div>
        </div>

        {selectionMode && !isReadOnly && (
          <div className="mt-2 flex items-center gap-2 overflow-x-auto pb-1 sm:flex-wrap sm:overflow-visible sm:pb-0">
            <Button
              variant="outline"
              size="sm"
              className="shrink-0"
              disabled={selectableFiles.length === 0}
              onClick={toggleSelectAll}
              aria-label={allSelected ? t('filesView.clearSelection') : t('filesView.selectAll')}
              title={allSelected ? t('filesView.clearSelection') : t('filesView.selectAll')}
            >
              <CheckSquareIcon size={16} />
              <span className="hidden sm:inline">
                {allSelected ? t('filesView.clearSelection') : t('filesView.selectAll')}
              </span>
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="shrink-0"
              disabled={selectableFiles.length === 0}
              onClick={invertSelection}
              aria-label={t('filesView.invertSelection')}
              title={t('filesView.invertSelection')}
            >
              <ArrowCounterClockwiseIcon size={16} />
              <span className="hidden sm:inline">{t('filesView.invertSelection')}</span>
            </Button>
            {selectedFlvPaths.length > 0 && (
              <Button
                size="sm"
                className="shrink-0"
                onClick={() => void addSelectedToRepair()}
                aria-label={t('filesView.addToRepair', { count: selectedFlvPaths.length })}
                title={t('filesView.addToRepair', { count: selectedFlvPaths.length })}
              >
                <WrenchIcon size={16} />
                <span className="hidden sm:inline">
                  {t('filesView.addToRepair', { count: selectedFlvPaths.length })}
                </span>
              </Button>
            )}
            <Button
              variant="destructive"
              size="sm"
              className="shrink-0"
              disabled={selectedFiles.length === 0 || isBatchDeleting}
              onClick={() => setDeleteDialogOpen(true)}
              aria-label={t('filesView.deleteSelected', { count: selectedFiles.length })}
              title={t('filesView.deleteSelected', { count: selectedFiles.length })}
            >
              <TrashSimpleIcon size={16} />
              <span className="hidden sm:inline">
                {t('filesView.deleteSelected', { count: selectedFiles.length })}
              </span>
            </Button>
            <span className="shrink-0 text-xs text-muted-foreground">
              {t('filesView.selectedCount', { count: selectedFiles.length })}
            </span>
          </div>
        )}

        <div className="mt-2 flex items-center gap-2 overflow-x-auto pb-1 sm:flex-wrap sm:overflow-visible sm:pb-0">
          <SortControl
            id="files-sort"
            label={t('viewControls.sortBy')}
            value={sortField}
            options={[
              { value: 'mtime', label: t('viewControls.sortMtime') },
              { value: 'name', label: t('viewControls.sortName') },
            ]}
            direction={sortDirection}
            onValueChange={(value) => setSortField(value as 'name' | 'mtime')}
            onDirectionChange={setSortDirection}
          />
          {currentPath ? (
            <>
              <div className="flex shrink-0 items-center gap-1.5 sm:gap-2">
                <Label
                  htmlFor="files-only-media"
                  className="cursor-pointer whitespace-nowrap text-xs text-muted-foreground sm:text-sm"
                >
                  <span className="hidden min-[420px]:inline">{t('filesView.onlyMedia')}</span>
                  <span className="min-[420px]:hidden">{t('filesView.onlyMediaShort')}</span>
                </Label>
                <Switch
                  id="files-only-media"
                  checked={onlyMedia}
                  onCheckedChange={(checked) => {
                    setOnlyMedia(checked)
                    saveOnlyMediaFiles(checked)
                  }}
                  aria-label={t('filesView.onlyMedia')}
                />
              </div>
              <RecordingBrowseTimeRange
                startValue={rangeStartInput}
                endValue={rangeEndInput}
                onStartChange={setRangeStartInput}
                onEndChange={setRangeEndInput}
                onClear={clearRangeFilter}
              />
            </>
          ) : null}
        </div>

        {currentPath ? (
          <div className="mt-1 space-y-1">
            <p className="truncate text-sm text-muted-foreground">{currentPath}</p>
            {rangeInvalid ? (
              <p className="text-xs text-destructive">{t('filesView.timeRangeInvalid')}</p>
            ) : null}
          </div>
        ) : null}
      </div>

      <div className="flex-1 min-h-0 relative overflow-hidden">
        <div
          ref={scrollContainerRef}
          className="absolute inset-0 overflow-y-auto"
          style={{ scrollbarGutter: 'stable' }}
        >
          <div className="p-4 pb-20">
            {isInitialLoading ? (
              <div className="flex items-center justify-center h-48">
                <div className="animate-pulse">
                  <div className="inline-flex items-center justify-center w-16 h-16 bg-primary/10 rounded-full">
                    <svg
                      className="w-8 h-8 text-primary"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke="currentColor"
                    >
                      <circle cx="12" cy="12" r="10" strokeWidth="2" />
                      <circle cx="12" cy="12" r="3" fill="currentColor" />
                    </svg>
                  </div>
                </div>
              </div>
            ) : displayFiles.length === 0 ? (
              <EmptyState
                icon={
                  <svg className="w-10 h-10 text-muted-foreground" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
                  </svg>
                }
                title={t('filesView.emptyTitle')}
                description={t('filesView.emptyDescription')}
              />
            ) : viewMode === 'list' ? (
              <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
                {displayFiles.map((file) => (
                  <FileListRow
                    key={file.path}
                    file={file}
                    onNavigate={handleNavigate}
                    onPlayback={handlePlayback}
                    currentPath={currentPath}
                    selectionMode={selectionMode}
                            selected={selection.has(file.path)}
                            onToggleSelected={!isReadOnly ? toggleSelection : undefined}
                  />
                ))}
              </div>
            ) : (
              <div style={{ height: `${Math.max(0, rowVirtualizer.getTotalSize() - GRID_GAP)}px`, position: 'relative' }}>
                {rowVirtualizer.getVirtualItems().map((virtualRow) => (
                  <div
                    key={virtualRow.key}
                    data-index={virtualRow.index}
                    ref={rowVirtualizer.measureElement}
                    style={{
                      position: 'absolute',
                      top: 0,
                      width: '100%',
                      transform: `translateY(${virtualRow.start}px)`,
                      paddingBottom: `${GRID_GAP}px`,
                    }}
                  >
                    <div className="grid gap-4" style={fixedColumnsStyle}>
                      {rows[virtualRow.index].map((file, colIndex) => {
                        if (!file) {
                          return hasNextPage
                            ? <FileCardSkeleton key={`row-placeholder-${virtualRow.index}-${colIndex}`} />
                            : <div key={`row-empty-${virtualRow.index}-${colIndex}`} />
                        }

                        return (
                          <FileCard
                            key={file.path}
                            file={file}
                            onNavigate={handleNavigate}
                            onDelete={handleFileDeleted}
                            onPlayback={handlePlayback}
                            currentPath={currentPath}
                            siblingFileNames={siblingFileNames}
                                    selectionMode={selectionMode}
                            selected={selection.has(file.path)}
                            onToggleSelected={!isReadOnly ? toggleSelection : undefined}
                          />
                        )
                      })}
                    </div>
                  </div>
                ))}
              </div>
            )}

            {!isInitialLoading && displayFiles.length > 0 && hasNextPage && (
              <div ref={loadMoreTriggerRef} className="mt-4" aria-hidden>
                <div className="grid gap-4" style={fixedColumnsStyle}>
                  {Array.from({ length: pageSize }).map((_, index) => (
                    <FileCardSkeleton key={`skeleton-${index}`} />
                  ))}
                </div>
              </div>
            )}

            {!isInitialLoading && displayFiles.length > 0 && (
              <p className="mt-4 text-center text-xs text-muted-foreground">
                {t('filesView.loadedCount', { loaded: displayFiles.length, total: displayTotal })}
              </p>
            )}
          </div>
        </div>
      </div>
      <Dialog open={deleteDialogOpen} onOpenChange={setDeleteDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('filesView.confirmDeleteTitle')}</DialogTitle>
            <DialogDescription>
              {t('filesView.confirmDeleteSelected', { count: selectedFiles.length })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDeleteDialogOpen(false)}>
              {t('fileCard.cancel')}
            </Button>
            <Button
              variant="destructive"
              disabled={isBatchDeleting}
              onClick={() => void confirmBatchDelete()}
            >
              {isBatchDeleting
                ? t('fileCard.deleting')
                : t('filesView.deleteSelected', { count: selectedFiles.length })}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {playbackFile ? (
        <PlaybackPlayerDialog
          open
          onOpenChange={(open) => {
            if (!open) setPlaybackFile(null)
          }}
          path={playbackFile.path}
          name={playbackFile.name}
        />
      ) : null}
    </div>
  )
}
