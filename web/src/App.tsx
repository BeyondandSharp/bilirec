import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { LoginView } from "@/components/LoginView";
import { AppHeader } from "@/components/AppHeader";
import { ViewerConnectScreen } from "@/components/ViewerConnectScreen";
import { RecordsView } from "@/components/RecordsView";
import { FilesView } from "@/components/FilesView";
import { ConvertsView } from "@/components/ConvertsView";
import { SubscribesView } from "@/components/SubscribesView";
import { SettingsView } from "@/components/SettingsView";
import { LogsView } from "@/components/LogsView";
import { RepairView } from "@/components/RepairView";
import { BilibiliAuthDialog } from "@/components/BilibiliAuthDialog";
import { AboutDialog } from "@/components/AboutDialog";
import { RestartDialog } from "@/components/RestartDialog";
import { AnalyticsConsentNotice } from "@/components/AnalyticsConsentNotice";
import { NetworkStatusToaster } from "@/components/NetworkStatusToaster";
import { BottomNav } from "@/components/BottomNav";
import { LeftSidebar } from "@/components/LeftSidebar";
import { useLanguage } from "@/hooks/use-language";
import { getStoredLanguage, type AppLanguage } from "@/lib/language";
import { Toaster } from "sonner";
import { motion, AnimatePresence } from "framer-motion";
import type { DiskUsage, ServerVersionResult } from "@/lib/types";
import { maybeAutoCheckVersion } from "@/lib/server-version";
import { RoleContext } from "@/lib/role-context";
import { ANALYTICS_ENABLED, isReportableAnalyticsVersion } from "@/lib/analytics";
import { VIEWER_MODE_ENABLED } from "@/lib/viewer-mode";
import { useAuthSession } from "@/hooks/use-auth-session";
import { useServiceWorkerUpdates } from "@/hooks/use-service-worker-updates";
import { useBilibiliAuth } from "@/hooks/use-bilibili-auth";
import { useLiveNotifications } from "@/hooks/use-live-notifications";
import { useDiskUsage } from "@/hooks/use-disk-usage";
import { useAnalyticsSession } from "@/hooks/use-analytics-session";
import { apiClient } from "@/lib/api";
import { ConvertCapabilityContext } from "@/lib/convert-capability";

type AppTab = "records" | "files" | "converts" | "repair" | "subscribe" | "settings" | "logs";

function getTabFromSearch(search: string): AppTab | null {
  const params = new URLSearchParams(search);
  const tab = params.get("tab");
  return tab === "records" ||
    tab === "files" ||
    tab === "converts" ||
    tab === "repair" ||
    tab === "subscribe" ||
    tab === "settings" ||
    tab === "logs"
    ? tab
    : null;
}

function getPinnedRoomFromSearch(search: string): number | null {
  const params = new URLSearchParams(search);
  const id = Number(params.get("pinnedRoom"));
  return isNaN(id) || id === 0 ? null : id;
}

function App() {
  const { t } = useTranslation();
  const { language, setLanguage } = useLanguage();

  const [activeTab, setActiveTab] = useState<AppTab>(
    VIEWER_MODE_ENABLED ? "files" : "records"
  );
  const [pinnedRoomId, setPinnedRoomId] = useState<number | null>(null);
  const [serverVersion, setServerVersion] = useState<ServerVersionResult | null>(null);
  const [diskUsage, setDiskUsage] = useState<DiskUsage | null>(null);
  const [isAboutOpen, setIsAboutOpen] = useState(false);
  const [isRestartOpen, setIsRestartOpen] = useState(false);
  const [isBilibiliDialogOpen, setIsBilibiliDialogOpen] = useState(false);
  const [isDocumentFullscreen, setIsDocumentFullscreen] = useState(false);
  // null/true = assume available until the backend tells us otherwise (older
  // backends have no /convert/providers endpoint). false hides the nav entry.
  const [canConvert, setCanConvert] = useState(true);
  const [convertProviders, setConvertProviders] = useState<string[]>([]);

  const auth = useAuthSession({
    onVersion: setServerVersion,
    onDiskUsage: setDiskUsage
  });
  const { isAuthenticated, isCheckingAuth, userRole, userName, isReadOnly } = auth;
  // Log viewer is admin-only (LOGS-VIEW contract).
  const canViewLogs = !isReadOnly;
  const canRepair = !isReadOnly;

  const bilibili = useBilibiliAuth(isAuthenticated, userRole);
  useDiskUsage(isAuthenticated, setDiskUsage);
  useServiceWorkerUpdates();
  useLiveNotifications(isAuthenticated, userRole);
  const { analyticsConsent, handleAnalyticsConsentChange } = useAnalyticsSession({
    isAuthenticated,
    serverVersion,
    onVersion: setServerVersion
  });

  const showServerOutdatedBadge =
    Boolean(serverVersion?.outdated && serverVersion?.checked);

  const handleServerVersionResult = (result: ServerVersionResult) => {
    setServerVersion(result);
  };

  const handleTabChange = (tab: AppTab) => {
    if (tab === "converts" && !canConvert) return;
    if (tab === "repair" && !canRepair) return;
    if (tab === "logs" && !canViewLogs) return;
    if (tab !== "subscribe") setPinnedRoomId(null);
    setActiveTab(tab);
  };

  useEffect(() => {
    if (!isAuthenticated) return;

    let cancelled = false;
    apiClient
      .getConvertProviders()
      .then((providers) => {
        if (!cancelled) {
          setCanConvert(providers.enabled !== false);
          setConvertProviders(providers.providers ?? []);
        }
      })
      .catch(() => {
        // Older backend without the endpoint: keep conversion visible.
      });

    return () => {
      cancelled = true;
    };
  }, [isAuthenticated]);

  useEffect(() => {
    if (!canConvert && activeTab === "converts") {
      setActiveTab(VIEWER_MODE_ENABLED ? "files" : "records");
    }
    if (!canViewLogs && activeTab === "logs") {
      setActiveTab(VIEWER_MODE_ENABLED ? "files" : "records");
    }
    if (!canRepair && activeTab === "repair") {
      setActiveTab(VIEWER_MODE_ENABLED ? "files" : "records");
    }
  }, [canConvert, canViewLogs, canRepair, activeTab]);

  useEffect(() => {
    if (isCheckingAuth || !isAuthenticated) {
      return;
    }

    maybeAutoCheckVersion(handleServerVersionResult, t);
  }, [isCheckingAuth, isAuthenticated, t]);

  useEffect(() => {
    let cancelled = false;

    const syncStoredLanguage = async () => {
      const storedLanguage = await getStoredLanguage();
      if (cancelled || !storedLanguage || storedLanguage === language) {
        return;
      }
      await setLanguage(storedLanguage);
    };

    syncStoredLanguage().catch((error) => {
      console.error("Failed to sync language preference:", error);
    });

    return () => {
      cancelled = true;
    };
  }, [language, setLanguage]);

  useEffect(() => {
    const tabFromUrl = getTabFromSearch(window.location.search);
    if (tabFromUrl) setActiveTab(tabFromUrl);
    const pinnedRoom = getPinnedRoomFromSearch(window.location.search);
    if (pinnedRoom) setPinnedRoomId(pinnedRoom);
    // Clean notification params from URL so a page refresh doesn't re-apply them
    if (tabFromUrl || pinnedRoom) {
      window.history.replaceState(null, "", window.location.pathname);
    }
  }, []);

  useEffect(() => {
    const syncFullscreenState = () => {
      setIsDocumentFullscreen(Boolean(document.fullscreenElement));
    };

    syncFullscreenState();
    document.addEventListener("fullscreenchange", syncFullscreenState);
    return () =>
      document.removeEventListener("fullscreenchange", syncFullscreenState);
  }, []);

  useEffect(() => {
    if (!("serviceWorker" in navigator)) return;

    const handleMessage = (event: MessageEvent) => {
      if (
        event.data?.type === "notification-click" &&
        event.data.tab === "subscribe"
      ) {
        setActiveTab("subscribe");
        if (typeof event.data.roomId === "number") {
          setPinnedRoomId(event.data.roomId);
        }
      }
    };

    navigator.serviceWorker.addEventListener("message", handleMessage);
    return () =>
      navigator.serviceWorker.removeEventListener("message", handleMessage);
  }, []);

  if (isCheckingAuth) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-background">
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
    );
  }

  if (!isAuthenticated) {
    if (VIEWER_MODE_ENABLED) {
      return (
        <>
          <ViewerConnectScreen
            onRetry={() => void auth.retryConnect()}
            isRetrying={isCheckingAuth}
          />
          <Toaster richColors position="top-center" />
        </>
      );
    }

    return (
      <>
        <LoginView onLoginSuccess={auth.handleLoginSuccess} />
        <Toaster richColors position="top-center" />
      </>
    );
  }

  return (
    <ConvertCapabilityContext.Provider value={{ canConvert, providers: convertProviders }}>
    <RoleContext.Provider value={{ role: userRole, userName, isReadOnly }}>
      <div className="min-h-screen bg-background overflow-x-hidden">
        <AppHeader
          diskUsage={diskUsage}
          serverVersion={serverVersion}
          bilibiliButtonVisible={bilibili.bilibiliButtonVisible}
          bilibiliAuthStatus={bilibili.bilibiliAuthStatus}
          onRefreshBilibiliStatus={bilibili.refreshBilibiliStatus}
          onOpenBilibiliDialog={() => setIsBilibiliDialogOpen(true)}
          onOpenAbout={() => setIsAboutOpen(true)}
          onOpenRestart={() => setIsRestartOpen(true)}
          showRestart={!isReadOnly}
          onLogout={auth.handleLogout}
          showLogout={!VIEWER_MODE_ENABLED}
          showServerOutdatedBadge={showServerOutdatedBadge}
        />

        {bilibili.bilibiliButtonVisible && (
          <BilibiliAuthDialog
            open={isBilibiliDialogOpen}
            onOpenChange={setIsBilibiliDialogOpen}
            initialStatus={bilibili.bilibiliAuthStatus}
            onStatusChange={bilibili.setBilibiliAuthStatus}
            onControllerUnsupported={() =>
              bilibili.setBilibiliButtonVisible(false)
            }
          />
        )}

        <AboutDialog
          open={isAboutOpen}
          onOpenChange={setIsAboutOpen}
          version={serverVersion}
          onVersionChange={setServerVersion}
          analyticsConsent={analyticsConsent}
          onAnalyticsConsentChange={handleAnalyticsConsentChange}
        />

        <RestartDialog open={isRestartOpen} onOpenChange={setIsRestartOpen} />

        {ANALYTICS_ENABLED &&
        analyticsConsent === null &&
        isReportableAnalyticsVersion(serverVersion?.current) &&
        !isDocumentFullscreen ? (
          <AnalyticsConsentNotice
            onConsentChange={handleAnalyticsConsentChange}
          />
        ) : null}

        <div className="h-[calc(100vh-73px)] overflow-x-hidden flex">
          <LeftSidebar
            activeTab={activeTab}
            onTabChange={handleTabChange}
            diskUsage={diskUsage}
            showConverts={canConvert}
            showRepair={canRepair}
            showLogs={canViewLogs}
          />

          <div className="min-w-0 flex-1">
            <AnimatePresence mode="wait">
              <motion.div
                key={activeTab}
                initial={{ opacity: 0, x: 20 }}
                animate={{ opacity: 1, x: 0 }}
                exit={{ opacity: 0, x: -20 }}
                transition={{ duration: 0.2 }}
                className="h-full"
              >
                {activeTab === "converts" && !canConvert ? null : activeTab === "records" ? (
                  <RecordsView />
                ) : activeTab === "files" ? (
                  <FilesView />
                ) : activeTab === "converts" ? (
                  <ConvertsView />
                ) : activeTab === "repair" && canRepair ? (
                  <RepairView />
                ) : activeTab === "settings" ? (
                  <SettingsView />
                ) : activeTab === "logs" ? (
                  canViewLogs ? <LogsView onOpenSettings={() => handleTabChange("settings")} /> : null
                ) : (
                  <SubscribesView pinnedRoomId={pinnedRoomId} />
                )}
              </motion.div>
            </AnimatePresence>
          </div>
        </div>

        <BottomNav
          activeTab={activeTab}
          onTabChange={handleTabChange}
          showConverts={canConvert}
          showRepair={canRepair}
          showLogs={canViewLogs}
        />
        <NetworkStatusToaster />
        <Toaster richColors position="top-center" />
      </div>
    </RoleContext.Provider>
    </ConvertCapabilityContext.Provider>
  );
}

export default App;