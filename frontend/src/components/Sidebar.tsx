import { Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import {
  SettingOutlined,
  ApiOutlined,
  BarChartOutlined,
  RobotOutlined,
  MonitorOutlined,
  DashboardOutlined,
  CloudServerOutlined,
  KeyOutlined,
  LogoutOutlined,
} from "@ant-design/icons";
import { clearToken } from "../api/client";

type Page = "providers" | "traffic" | "settings" | "intent" | "monitor" | "dashboard" | "channels" | "tokens";

interface NavItem {
  key: Page;
  icon: React.ReactNode;
  labelKey: string;
}

export default function Sidebar({ active, onChange }: { active: Page; onChange: (p: Page) => void }) {
  const { t } = useTranslation();

  const navItems: NavItem[] = [
    { key: "dashboard", icon: <DashboardOutlined />, labelKey: "nav.dashboard" },
    { key: "channels", icon: <CloudServerOutlined />, labelKey: "nav.channels" },
    { key: "tokens", icon: <KeyOutlined />, labelKey: "nav.tokens" },
    { key: "providers", icon: <ApiOutlined />, labelKey: "nav.providers" },
    { key: "traffic", icon: <BarChartOutlined />, labelKey: "nav.traffic" },
    { key: "monitor", icon: <MonitorOutlined />, labelKey: "nav.monitor" },
    { key: "intent", icon: <RobotOutlined />, labelKey: "nav.intent" },
    { key: "settings", icon: <SettingOutlined />, labelKey: "nav.settings" },
  ];

  const handleLogout = () => {
    clearToken();
    window.location.reload();
  };

  return (
    <div
      style={{
        width: 64,
        background: "var(--bg-primary)",
        borderRight: "1px solid var(--border-light)",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        paddingTop: 12,
        flexShrink: 0,
        gap: 2,
      }}
    >
      {navItems.map((item) => {
        const isActive = active === item.key;
        return (
          <Tooltip key={item.key} title={t(item.labelKey)} placement="right" mouseEnterDelay={0.5}>
            <button
              onClick={() => onChange(item.key)}
              style={{
                width: 42,
                height: 42,
                borderRadius: 10,
                border: "none",
                background: isActive ? "var(--bg-active)" : "transparent",
                color: isActive ? "var(--accent)" : "var(--text-muted)",
                cursor: "pointer",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                fontSize: 19,
                transition: "all 0.15s",
                position: "relative",
                outline: "none",
              }}
              onMouseEnter={(e) => {
                if (!isActive) {
                  e.currentTarget.style.color = "var(--text-secondary)";
                  e.currentTarget.style.background = "var(--bg-elevated)";
                }
              }}
              onMouseLeave={(e) => {
                if (!isActive) {
                  e.currentTarget.style.color = "var(--text-muted)";
                  e.currentTarget.style.background = "transparent";
                }
              }}
            >
              {item.icon}
              {isActive && (
                <div
                  style={{
                    position: "absolute",
                    left: -4,
                    top: "30%",
                    height: "40%",
                    width: 3,
                    borderRadius: "0 2px 2px 0",
                    background: "var(--accent)",
                  }}
                />
              )}
            </button>
          </Tooltip>
        );
      })}
      <div style={{ flex: 1 }} />
      {/* 退出登录 */}
      <Tooltip title={t("nav.logout")} placement="right" mouseEnterDelay={0.5}>
        <button
          onClick={handleLogout}
          style={{
            width: 42,
            height: 42,
            borderRadius: 10,
            border: "none",
            background: "transparent",
            color: "var(--text-muted)",
            cursor: "pointer",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            fontSize: 19,
            transition: "all 0.15s",
            outline: "none",
          }}
          onMouseEnter={(e) => {
            e.currentTarget.style.color = "#ff4d4f";
            e.currentTarget.style.background = "var(--bg-elevated)";
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.color = "var(--text-muted)";
            e.currentTarget.style.background = "transparent";
          }}
        >
          <LogoutOutlined />
        </button>
      </Tooltip>
      <div style={{ paddingBottom: 14, fontSize: 9, color: "var(--text-muted)", fontWeight: 600, letterSpacing: 1 }}>
        {t("app.version")}
      </div>
    </div>
  );
}
