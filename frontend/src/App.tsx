import { useEffect, useState, lazy, Suspense } from "react";
import { Spin } from "antd";
import ErrorBoundary from "./components/ErrorBoundary";
import TitleBar from "./components/TitleBar";
import Sidebar from "./components/Sidebar";
import StatusBar from "./components/StatusBar";
import SetupBanner from "./components/SetupBanner";
import { isLoggedIn } from "./api/client";
import { trafficApi } from "./api";

// 代码分割：页面组件按需加载，减少首屏 bundle 体积。
const Providers = lazy(() => import("./components/Providers"));
const Settings = lazy(() => import("./components/Settings"));
const IntentPanel = lazy(() => import("./components/IntentPanel"));
const MonitorPanel = lazy(() => import("./components/MonitorPanel"));
const TrafficMonitor = lazy(() => import("./pages/TrafficMonitor"));
const Dashboard = lazy(() => import("./pages/Dashboard"));
const Login = lazy(() => import("./pages/Login"));
const Channels = lazy(() => import("./pages/Channels"));
const Tokens = lazy(() => import("./pages/Tokens"));

type Page = "providers" | "traffic" | "settings" | "intent" | "monitor" | "dashboard" | "channels" | "tokens";

/** 页面加载中的占位 spinner */
function PageLoader() {
  return (
    <div style={{ display: "flex", justifyContent: "center", alignItems: "center", height: "100%" }}>
      <Spin size="large" />
    </div>
  );
}

export default function App() {
  const [page, setPage] = useState<Page>("dashboard");
  const [loggedIn, setLoggedIn] = useState(isLoggedIn());
  const [trafficStats, setTrafficStats] = useState({ requests: 0, todayCost: 0 });

  // Fetch real traffic stats for status bar
  useEffect(() => {
    if (!loggedIn) return;

    const fetchStatus = () => {
      trafficApi.getOverview()
        .then(data => {
          if (data.available) {
            setTrafficStats({
              requests: data.total?.requests ?? 0,
              todayCost: data.today?.cost_usd ?? 0,
            });
          }
        })
        .catch((err) => {
          console.warn("[App] 获取状态数据失败:", (err as Error).message);
        });
    };
    fetchStatus();
    const interval = setInterval(fetchStatus, 30_000);
    return () => clearInterval(interval);
  }, [loggedIn]);

  // 未登录显示登录页面
  if (!loggedIn) {
    return (
      <ErrorBoundary>
        <Suspense fallback={<PageLoader />}>
          <Login onLogin={() => setLoggedIn(true)} />
        </Suspense>
      </ErrorBoundary>
    );
  }

  return (
    <ErrorBoundary>
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          height: "100vh",
          width: "100vw",
          overflow: "hidden",
          background: "var(--bg-primary)",
        }}
      >
        {/* Custom Title Bar */}
        <TitleBar />

        {/* Setup banner (shown on first launch) */}
        <SetupBanner />

        {/* Main body: Sidebar + Content */}
        <div style={{ display: "flex", flex: 1, overflow: "hidden" }}>
          <Sidebar active={page} onChange={setPage} />

          {/* Content area */}
          <div
            style={{
              flex: 1,
              overflowX: "hidden",
              overflowY: "auto",
              background: "var(--bg-secondary)",
              minWidth: 0,
            }}
          >
            <Suspense fallback={<PageLoader />}>
              {page === "dashboard" && <Dashboard />}
              {page === "channels" && <Channels />}
              {page === "tokens" && <Tokens />}
              {page === "providers" && <Providers />}
              {page === "traffic" && <TrafficMonitor />}
              {page === "intent" && <IntentPanel />}
              {page === "monitor" && <MonitorPanel />}
              {page === "settings" && <Settings />}
            </Suspense>
          </div>
        </div>

        {/* Status Bar */}
        <StatusBar
          requestCount={trafficStats.requests}
          todayCost={trafficStats.todayCost}
        />
      </div>
    </ErrorBoundary>
  );
}
