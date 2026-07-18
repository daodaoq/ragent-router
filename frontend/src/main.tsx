import React, { useEffect } from "react";
import ReactDOM from "react-dom/client";
import { ConfigProvider, theme } from "antd";
import enUS from "antd/locale/en_US";
import zhCN from "antd/locale/zh_CN";
import App from "./App";
import "./i18n";
import "./index.css";
import i18n from "./i18n";
import { useThemeStore } from "./stores/theme";

const antdLocales: Record<string, typeof enUS> = { en: enUS, zh: zhCN };

function Root() {
  const [locale, setLocale] = React.useState(antdLocales[i18n.language] || zhCN);
  const mode = useThemeStore((s) => s.mode);

  useEffect(() => {
    const handler = (lng: string) => setLocale(antdLocales[lng] || zhCN);
    i18n.on("languageChanged", handler);
    return () => { i18n.off("languageChanged", handler); };
  }, []);

  // Apply theme — set data-theme attribute AND CSS variables directly on <html>
  useEffect(() => {
    const html = document.documentElement;
    html.setAttribute("data-theme", mode);
    const vars = mode === "dark"
      ? {
          "--bg-primary": "#0a0a1a",
          "--bg-secondary": "#0e0e24",
          "--bg-card": "#141428",
          "--bg-elevated": "#1a1a35",
          "--bg-active": "#252845",
          "--border-color": "#1a1a40",
          "--border-light": "#2a2a45",
          "--text-primary": "#e0e0e0",
          "--text-secondary": "#888",
          "--text-muted": "#555",
          "--accent": "#818cf8",
          "--accent-light": "#a5b4fc",
          "--green": "#34d399",
          "--red": "#f87171",
          "--orange": "#fbbf24",
        }
      : {
          "--bg-primary": "#ffffff",
          "--bg-secondary": "#f8f9fa",
          "--bg-card": "#ffffff",
          "--bg-elevated": "#f0f1f3",
          "--bg-active": "#eef2ff",
          "--border-color": "#e8e9eb",
          "--border-light": "#e0e1e3",
          "--text-primary": "#1a1a2e",
          "--text-secondary": "#6b7280",
          "--text-muted": "#9ca3af",
          "--accent": "#6366f1",
          "--accent-light": "#818cf8",
          "--green": "#10b981",
          "--red": "#ef4444",
          "--orange": "#f59e0b",
        };
    for (const [k, v] of Object.entries(vars)) {
      html.style.setProperty(k, v);
    }
  }, [mode]);

  const isDark = mode === "dark";

  return (
    <ConfigProvider
      locale={locale}
      theme={{
        algorithm: isDark ? theme.darkAlgorithm : theme.defaultAlgorithm,
        token: {
          colorPrimary: "#6366f1",
          borderRadius: 8,
          fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
          fontSize: 13,
        },
      }}
    >
      <App />
    </ConfigProvider>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <Root />
  </React.StrictMode>
);
