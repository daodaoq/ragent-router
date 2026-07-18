import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n from "../i18n";

// @ts-ignore - injected by preload
const api = window.electronAPI;

export default function TitleBar() {
  const { t } = useTranslation();
  const [maximized, setMaximized] = useState(false);
  const [backendOnline, setBackendOnline] = useState(false);
  const [langOpen, setLangOpen] = useState(false);

  const currentLang = i18n.language.startsWith("zh") ? "zh" : "en";

  useEffect(() => {
    if (!api) return;
    api.isMaximized().then(setMaximized);
    api.onMaximizeChange((v: boolean) => setMaximized(v));
    api.getBackendStatus().then((s: { online: boolean }) => setBackendOnline(s.online));
    api.onBackendStatus((s: { online: boolean }) => setBackendOnline(s.online));
  }, []);

  const switchLang = (lng: string) => {
    i18n.changeLanguage(lng);
    setLangOpen(false);
  };

  const btnBase: React.CSSProperties = {
    width: 46,
    height: 38,
    border: "none",
    background: "transparent",
    color: "var(--text-muted)",
    cursor: "pointer",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    fontSize: 14,
    transition: "all 0.15s",
    outline: "none",
  };

  return (
    <div
      className="titlebar-drag"
      style={{
        height: 38,
        background: "var(--bg-primary)",
        borderBottom: "1px solid var(--border-light)",
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        flexShrink: 0,
        zIndex: 100,
      }}
    >
      {/* Left: App Icon + Title */}
      <div style={{ display: "flex", alignItems: "center", paddingLeft: 16, gap: 10 }}>
        <div
          style={{
            width: 20,
            height: 20,
            borderRadius: 5,
            background: "#6366f1",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            fontSize: 11,
            fontWeight: 700,
            color: "#fff",
          }}
        >
          R
        </div>
        <span style={{ color: "var(--text-primary)", fontSize: 12, fontWeight: 600, letterSpacing: "-0.2px" }}>
          {t("app.title")}
        </span>
        <span
          style={{
            width: 6,
            height: 6,
            borderRadius: "50%",
            background: backendOnline ? "var(--green)" : "var(--red)",
            transition: "background 0.3s",
          }}
          title={backendOnline ? t("titlebar.backendOnline") : t("titlebar.backendOffline")}
        />
      </div>

      {/* Center: drag region */}
      <div style={{ flex: 1 }} />

      {/* Language Switcher */}
      <div className="titlebar-no-drag" style={{ position: "relative", marginRight: 4 }}>
        <button
          onClick={() => setLangOpen(!langOpen)}
          style={{
            ...btnBase,
            width: 36,
            fontSize: 11,
            fontWeight: 600,
            color: "var(--text-secondary)",
            background: langOpen ? "var(--bg-elevated)" : "transparent",
            borderRadius: 6,
          }}
          onMouseEnter={(e) => {
            if (!langOpen) e.currentTarget.style.background = "var(--bg-elevated)";
          }}
          onMouseLeave={(e) => {
            if (!langOpen) e.currentTarget.style.background = "transparent";
          }}
          title={t("language.switch")}
        >
          {currentLang === "zh" ? "中" : "EN"}
        </button>
        {langOpen && (
          <>
            <div
              style={{ position: "fixed", inset: 0, zIndex: 1 }}
              onClick={() => setLangOpen(false)}
            />
            <div
              style={{
                position: "absolute",
                top: 40,
                right: 0,
                background: "var(--bg-card)",
                border: "1px solid var(--border-light)",
                borderRadius: 8,
                boxShadow: "0 4px 16px rgba(0,0,0,0.15)",
                zIndex: 200,
                overflow: "hidden",
                minWidth: 120,
              }}
            >
              {[
                { key: "zh", label: "🇨🇳 中文" },
                { key: "en", label: "🇺🇸 English" },
              ].map((item) => (
                <button
                  key={item.key}
                  onClick={() => switchLang(item.key)}
                  style={{
                    display: "block",
                    width: "100%",
                    padding: "8px 14px",
                    border: "none",
                    background: currentLang === item.key ? "var(--bg-active)" : "transparent",
                    color: currentLang === item.key ? "var(--accent)" : "var(--text-primary)",
                    cursor: "pointer",
                    fontSize: 12,
                    textAlign: "left",
                    outline: "none",
                    fontWeight: currentLang === item.key ? 600 : 400,
                  }}
                  onMouseEnter={(e) => {
                    if (currentLang !== item.key) e.currentTarget.style.background = "var(--bg-secondary)";
                  }}
                  onMouseLeave={(e) => {
                    if (currentLang !== item.key) e.currentTarget.style.background = "transparent";
                  }}
                >
                  {item.label}
                </button>
              ))}
            </div>
          </>
        )}
      </div>

      {/* Right: Window Controls */}
      <div className="titlebar-no-drag" style={{ display: "flex", height: "100%" }}>
        <button
          style={btnBase}
          onClick={() => api?.minimize()}
          onMouseEnter={(e) => (e.currentTarget.style.background = "var(--bg-elevated)")}
          onMouseLeave={(e) => (e.currentTarget.style.background = "transparent")}
          title={t("titlebar.minimize")}
        >
          <svg width="12" height="12" viewBox="0 0 12 12">
            <rect x="1" y="5.5" width="10" height="1" rx="0.5" fill="currentColor" />
          </svg>
        </button>

        <button
          style={btnBase}
          onClick={() => api?.maximize()}
          onMouseEnter={(e) => (e.currentTarget.style.background = "var(--bg-elevated)")}
          onMouseLeave={(e) => (e.currentTarget.style.background = "transparent")}
          title={maximized ? t("titlebar.restore") : t("titlebar.maximize")}
        >
          {maximized ? (
            <svg width="12" height="12" viewBox="0 0 12 12">
              <rect x="2.5" y="-0.5" width="8" height="8" rx="1.5" fill="#fff" stroke="currentColor" strokeWidth="1" />
              <rect x="-0.5" y="3.5" width="8" height="8" rx="1.5" fill="#fff" stroke="currentColor" strokeWidth="1" />
            </svg>
          ) : (
            <svg width="12" height="12" viewBox="0 0 12 12">
              <rect x="1.5" y="1.5" width="9" height="9" rx="1.5" fill="none" stroke="currentColor" strokeWidth="1.2" />
            </svg>
          )}
        </button>

        <button
          style={{ ...btnBase, color: "var(--text-muted)" }}
          onClick={() => api?.close()}
          onMouseEnter={(e) => {
            e.currentTarget.style.background = "#ef4444";
            e.currentTarget.style.color = "#fff";
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.background = "transparent";
            e.currentTarget.style.color = "#9ca3af";
          }}
          title={t("titlebar.close")}
        >
          <svg width="12" height="12" viewBox="0 0 12 12">
            <path d="M2 2l8 8M10 2L2 10" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
          </svg>
        </button>
      </div>
    </div>
  );
}
