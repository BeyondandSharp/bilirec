import { cn } from '@/lib/utils'
import { SwapIcon, BellIcon, GearSixIcon, TerminalIcon } from '@phosphor-icons/react'
import { useTranslation } from 'react-i18next'

type AppTab = 'records' | 'files' | 'converts' | 'subscribe' | 'settings' | 'logs'

interface BottomNavProps {
  activeTab: AppTab
  onTabChange: (tab: AppTab) => void
  showConverts?: boolean
  showLogs?: boolean
}

export function BottomNav({ activeTab, onTabChange, showConverts = true, showLogs = true }: BottomNavProps) {
  const { t } = useTranslation()

  return (
    <nav
      className="fixed bottom-0 left-0 right-0 bg-card/95 backdrop-blur-md border-t border-border safe-area-bottom z-50 lg:hidden select-none"
      style={{ touchAction: 'none', userSelect: 'none', overscrollBehavior: 'contain' }}
    >
      <div className="flex items-center justify-around h-16">
        <button
          onClick={() => onTabChange('records')}
          className={cn(
            'flex flex-col items-center justify-center gap-1 flex-1 h-full transition-colors',
            activeTab === 'records' ? 'text-primary' : 'text-muted-foreground'
          )}
        >
          <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <circle cx="12" cy="12" r="10" strokeWidth="2" />
            <circle cx="12" cy="12" r="3" fill="currentColor" />
          </svg>
          <span className="text-xs font-medium">{t('nav.records')}</span>
        </button>
        <button
          onClick={() => onTabChange('files')}
          className={cn(
            'flex flex-col items-center justify-center gap-1 flex-1 h-full transition-colors',
            activeTab === 'files' ? 'text-primary' : 'text-muted-foreground'
          )}
        >
          <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
          </svg>
          <span className="text-xs font-medium">{t('nav.files')}</span>
        </button>
        {showConverts && (
          <button
            onClick={() => onTabChange('converts')}
            className={cn(
              'flex flex-col items-center justify-center gap-1 flex-1 h-full transition-colors',
              activeTab === 'converts' ? 'text-primary' : 'text-muted-foreground'
            )}
          >
            <span className="w-6 h-6 flex items-center justify-center">
              <SwapIcon size={18} weight="bold" />
            </span>
            <span className="text-xs font-medium">{t('nav.converts')}</span>
          </button>
        )}
        <button
          onClick={() => onTabChange('subscribe')}
          className={cn(
            'flex flex-col items-center justify-center gap-1 flex-1 h-full transition-colors',
            activeTab === 'subscribe' ? 'text-primary' : 'text-muted-foreground'
          )}
        >
          <span className="w-6 h-6 flex items-center justify-center">
            <BellIcon size={18} weight="bold" />
          </span>
          <span className="text-xs font-medium">{t('nav.subscribe')}</span>
        </button>
        {showLogs && (
          <button
            onClick={() => onTabChange('logs')}
            className={cn(
              'flex flex-col items-center justify-center gap-1 flex-1 h-full transition-colors',
              activeTab === 'logs' ? 'text-primary' : 'text-muted-foreground'
            )}
          >
            <span className="w-6 h-6 flex items-center justify-center">
              <TerminalIcon size={18} weight="bold" />
            </span>
            <span className="text-xs font-medium">{t('nav.logs')}</span>
          </button>
        )}
        <button
          onClick={() => onTabChange('settings')}
          className={cn(
            'flex flex-col items-center justify-center gap-1 flex-1 h-full transition-colors',
            activeTab === 'settings' ? 'text-primary' : 'text-muted-foreground'
          )}
        >
          <span className="w-6 h-6 flex items-center justify-center">
            <GearSixIcon size={18} weight="bold" />
          </span>
          <span className="text-xs font-medium">{t('nav.settings')}</span>
        </button>
      </div>
    </nav>
  )
}
