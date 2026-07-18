import { useEffect, useState } from "react";
import TitleBar from "./components/TitleBar";
import Sidebar from "./components/Sidebar";
import StatusBar from "./components/StatusBar";
import SetupBanner from "./components/SetupBanner";
import TrafficMonitor from "./pages/TrafficMonitor";
import Providers from "./components/Providers";
import Settings from "./components/Settings";
import IntentPanel from "./components/IntentPanel";
import MonitorPanel from "./components/MonitorPanel";

type Page = "providers" | "traffic" | "settings" | "intent" | "monitor";

export default function App() {
  const [page, setPage] = useState<Page>("providers");
  const [trafficStats, setTrafficStats] = useState({ requests: 0, todayCost: 0 });

  // Fetch real traffic stats for status bar
  useEffect(() => {
    const fetchStatus = () => {
      fetch("http://localhost:15722/api/traffic/overview")
        .then(r => r.json())
        .then(data => {
          if (data.available) {
            setTrafficStats({
              requests: data.total?.requests ?? 0,
              todayCost: data.today?.cost_usd ?? 0,
            });
          }
        })
        .catch(() => {});
    };
    fetchStatus();
    const interval = setInterval(fetchStatus, 30_000);
    return () => clearInterval(interval);
  }, []);

  return (
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
          {page === "providers" && <Providers />}
          {page === "traffic" && <TrafficMonitor />}
          {page === "intent" && <IntentPanel />}
          {page === "monitor" && <MonitorPanel />}
          {page === "settings" && <Settings />}
        </div>
      </div>

      {/* Status Bar */}
      <StatusBar
        requestCount={trafficStats.requests}
        todayCost={trafficStats.todayCost}
      />
    </div>
  );
}
