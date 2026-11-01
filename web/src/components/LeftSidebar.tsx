import { cn } from '@/lib/utils'
import { FileIcon, WrenchIcon, SwapIcon, BellIcon, GearSixIcon, TerminalIcon } from '@phosphor-icons/react'
import { DiskUsageDisplay } from './DiskUsageDisplay'
import type { DiskUsage } from '@/lib/types'
import { useTranslation } from 'react-i18next'

type AppTab = 'records' | 'files' | 'converts' | 'repair' | 'subscribe' | 'settings' | 'logs'

type LeftSidebarProps = {
  activeTab: AppTab
  onTabChange: (t: AppTab) => void
  diskUsage?: DiskUsage | null
  showConverts?: boolean
  showRepair?: boolean
  showLogs?: boolean
}

export function LeftSidebar({ activeTab, onTabChange, diskUsage, showConverts = true, showRepair = true, showLogs = true }: LeftSidebarProps) {
  const { t } = useTranslation()

  return (
    <aside className="hidden w-56 shrink-0 lg:flex lg:flex-col h-full p-4 border-r border-border bg-sidebar/95 text-sidebar-foreground">
      <nav className="flex flex-col gap-2 mt-2 flex-1">
        <button
          onClick={() => onTabChange('records')}
          aria-pressed={activeTab === 'records'}
          className={cn(
            'flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium transition-colors w-full cursor-pointer',
            activeTab === 'records'
              ? 'text-primary bg-primary/10'
              : 'text-muted-foreground hover:text-primary hover:bg-secondary/10'
          )}
        >
          <span className="shrink-0">
            <svg className="w-5 h-5 text-primary" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <circle cx="12" cy="12" r="10" strokeWidth="2" />
              <circle cx="12" cy="12" r="3" fill="currentColor" />
            </svg>
          </span>
          <span>{t('nav.records')}</span>
        </button>

        <button
          onClick={() => onTabChange('files')}
          aria-pressed={activeTab === 'files'}
          className={cn(
            'flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium transition-colors w-full cursor-pointer',
            activeTab === 'files'
              ? 'text-primary bg-primary/10'
              : 'text-muted-foreground hover:text-primary hover:bg-secondary/10'
          )}
        >
          <span className="shrink-0"><FileIcon size={18} weight="bold" /></span>
          <span>{t('nav.files')}</span>
        </button>

        {showRepair && (
          <button
            onClick={() => onTabChange('repair')}
            aria-pressed={activeTab === 'repair'}
            className={cn(
              'flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium transition-colors w-full cursor-pointer',
              activeTab === 'repair'
                ? 'text-primary bg-primary/10'
                : 'text-muted-foreground hover:text-primary hover:bg-secondary/10'
            )}
          >
            <span className="shrink-0"><WrenchIcon size={18} weight="bold" /></span>
            <span>{t('nav.repair')}</span>
          </button>
        )}

        {showConverts && (
          <button
            onClick={() => onTabChange('converts')}
            aria-pressed={activeTab === 'converts'}
            className={cn(
              'flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium transition-colors w-full cursor-pointer',
              activeTab === 'converts'
                ? 'text-primary bg-primary/10'
                : 'text-muted-foreground hover:text-primary hover:bg-secondary/10'
            )}
          >
            <span className="shrink-0"><SwapIcon size={18} weight="bold" /></span>
            <span>{t('nav.converts')}</span>
          </button>
        )}

        <button
          onClick={() => onTabChange('subscribe')}
          aria-pressed={activeTab === 'subscribe'}
          className={cn(
            'flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium transition-colors w-full cursor-pointer',
            activeTab === 'subscribe'
              ? 'text-primary bg-primary/10'
              : 'text-muted-foreground hover:text-primary hover:bg-secondary/10'
          )}
        >
          <span className="shrink-0"><BellIcon size={18} weight="bold" /></span>
          <span>{t('nav.subscribe')}</span>
        </button>

        {showLogs && (
          <button
            onClick={() => onTabChange('logs')}
            aria-pressed={activeTab === 'logs'}
            className={cn(
              'flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium transition-colors w-full cursor-pointer',
              activeTab === 'logs'
                ? 'text-primary bg-primary/10'
                : 'text-muted-foreground hover:text-primary hover:bg-secondary/10'
            )}
          >
            <span className="shrink-0"><TerminalIcon size={18} weight="bold" /></span>
            <span>{t('nav.logs')}</span>
          </button>
        )}

        <button
          onClick={() => onTabChange('settings')}
          aria-pressed={activeTab === 'settings'}
          className={cn(
            'flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium transition-colors w-full cursor-pointer',
            activeTab === 'settings'
              ? 'text-primary bg-primary/10'
              : 'text-muted-foreground hover:text-primary hover:bg-secondary/10'
          )}
        >
          <span className="shrink-0"><GearSixIcon size={18} weight="bold" /></span>
          <span>{t('nav.settings')}</span>
        </button>
      </nav>

      {diskUsage && (
        <div className="pt-4 border-t border-border">
          <DiskUsageDisplay diskUsage={diskUsage} />
        </div>
      )}
    </aside>
  )
} 
