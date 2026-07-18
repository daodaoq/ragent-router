import { useEffect, useState } from "react";
import {
  Card, Button, Typography, Divider, Switch, message, Space, Tag, Descriptions, Segmented, Statistic, Row, Col,
} from "antd";
import {
  ReloadOutlined, RollbackOutlined, BulbOutlined, GlobalOutlined,
  SettingOutlined, InfoCircleOutlined, DatabaseOutlined,
  FolderOpenOutlined, CheckCircleOutlined, CloseCircleOutlined,
  ThunderboltOutlined, CloudServerOutlined, FileTextOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useThemeStore } from "../stores/theme";
import i18n from "../i18n";
import PageHelp from "./PageHelp";

const { Text, Title } = Typography;

// @ts-ignore
const api = window.electronAPI;

interface CCStatus {
  ccswitch_available: boolean;
  proxy_configured: boolean;
  current_provider: string | null;
  proxy_base_url: string;
}

interface CCDetails {
  available: boolean;
  path: string;
  exe_path: string;
  exe_exists: boolean;
  db_size_mb: number;
}

interface TrafficStatus {
  available: boolean;
  total_records: number;
  last_request: string | null;
}

export default function Settings() {
  const { t } = useTranslation();
  const lang = i18n.language.startsWith("zh") ? "zh" : "en";
  const { mode: themeMode, setMode } = useThemeStore();

  // Electron settings
  const [backendPort, setBackendPort] = useState(15722);
  const [closeToTray, setCloseToTray] = useState(true);
  const [autoStartBackend, setAutoStartBackend] = useState(true);
  const [saving, setSaving] = useState(false);
  const [backendOnline, setBackendOnline] = useState(false);

  // CC Switch
  const [ccStatus, setCcStatus] = useState<CCStatus | null>(null);
  const [ccDetails, setCcDetails] = useState<CCDetails | null>(null);
  const [trafficStatus, setTrafficStatus] = useState<TrafficStatus | null>(null);
  const [reverting, setReverting] = useState(false);
  const [applyLoading, setApplyLoading] = useState(false);

  // App info
  const [appInfo, setAppInfo] = useState<any>(null);

  useEffect(() => {
    if (api) {
      api.getSettings().then((s: any) => {
        if (s) {
          setBackendPort(s.backendPort || 15722);
          setCloseToTray(s.closeToTray !== false);
          setAutoStartBackend(s.autoStartBackend !== false);
        }
      });
      api.getAppInfo().then(setAppInfo);
      api.getBackendStatus().then((s: { online: boolean; port: number }) => {
        setBackendOnline(s.online);
        setBackendPort(s.port);
      });
    }

    // Fetch CC Switch status
    Promise.all([
      fetch("http://localhost:15722/api/setup/status").then(r => r.json()).catch(() => null),
      fetch("http://localhost:15722/api/ccswitch/status").then(r => r.json()).catch(() => null),
      fetch("http://localhost:15722/api/traffic/status").then(r => r.json()).catch(() => null),
    ]).then(([setup, details, traffic]) => {
      setCcStatus(setup);
      setCcDetails(details);
      setTrafficStatus(traffic);
    });
  }, []);

  const saveElectronSettings = async () => {
    if (!api) return;
    setSaving(true);
    try {
      await api.saveSettings({ backendPort, closeToTray, autoStartBackend });
      message.success(t("settings.saveSuccess"));
    } catch { message.error(t("settings.saveFail")); }
    setSaving(false);
  };

  const handleApplySetup = async () => {
    setApplyLoading(true);
    try {
      const res = await fetch("http://localhost:15722/api/setup/apply", { method: "POST" });
      const data = await res.json();
      if (data.success) {
        setCcStatus(prev => prev ? { ...prev, proxy_configured: true } : null);
        message.success(lang === "zh" ? "代理已配置成功" : "Proxy configured successfully");
      } else {
        message.error(data.detail || data.message || "Failed");
      }
    } catch { message.error(lang === "zh" ? "配置失败" : "Setup failed"); }
    setApplyLoading(false);
  };

  const handleRevert = async () => {
    setReverting(true);
    try {
      const res = await fetch("http://localhost:15722/api/setup/revert", { method: "POST" });
      const data = await res.json();
      if (data.success) {
        setCcStatus(prev => prev ? { ...prev, proxy_configured: false } : null);
        message.success(
          lang === "zh" ? `已恢复到 ${data.restored_provider}` : `Reverted to ${data.restored_provider}`
        );
      }
    } catch { message.error(lang === "zh" ? "回退失败" : "Revert failed"); }
    setReverting(false);
  };

  const serviceLabel = (name: string) => lang === "zh"
    ? { "Claude": "Claude", "OpenAI": "OpenAI", "Gemini": "Gemini" }[name] || name
    : name;

  return (
    <div style={{ padding: 24, maxWidth: 720 }}>
      <Title level={4} style={{ color: "var(--text-primary)", marginBottom: 24 }}>
        <SettingOutlined style={{ marginRight: 8 }} />
        {t("settings.title")}
        <PageHelp page="settings" />
      </Title>

      {/* ── System Status ────────────────────────────────────────── */}
      <Card
        bordered={false}
        style={{ marginBottom: 16, background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}
        bodyStyle={{ padding: "16px 20px" }}
      >
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
          <Text strong style={{ color: "var(--text-primary)", fontSize: 14 }}>
            <CloudServerOutlined style={{ marginRight: 6 }} />
            {lang === "zh" ? "系统状态" : "System Status"}
          </Text>
        </div>
        <Row gutter={[16, 12]} style={{ marginTop: 12 }}>
          <Col span={8}>
            <Card size="small" bordered={false} style={{ background: backendOnline ? "#f0fdf4" : "#fef2f2", borderRadius: 8 }}>
              <Statistic
                title={<Text style={{ fontSize: 11, color: "var(--text-secondary)" }}>RAgent Router</Text>}
                value={backendOnline ? (lang === "zh" ? "运行中" : "Online") : (lang === "zh" ? "离线" : "Offline")}
                valueStyle={{ fontSize: 16, fontWeight: 600, color: backendOnline ? "var(--green)" : "var(--red)" }}
                prefix={backendOnline ? <CheckCircleOutlined /> : <CloseCircleOutlined />}
              />
              <Text style={{ fontSize: 10, color: "var(--text-muted)" }}>:{backendPort}</Text>
            </Card>
          </Col>
          <Col span={8}>
            <Card size="small" bordered={false} style={{ background: ccDetails?.available ? "#f0fdf4" : "#fffbeb", borderRadius: 8 }}>
              <Statistic
                title={<Text style={{ fontSize: 11, color: "var(--text-secondary)" }}>CC Switch</Text>}
                value={ccDetails?.available ? (lang === "zh" ? "已连接" : "Connected") : (lang === "zh" ? "未检测到" : "Not Found")}
                valueStyle={{ fontSize: 16, fontWeight: 600, color: ccDetails?.available ? "var(--green)" : "var(--orange)" }}
                prefix={ccDetails?.available ? <CheckCircleOutlined /> : <CloseCircleOutlined />}
              />
              {ccDetails?.db_size_mb !== undefined && (
                <Text style={{ fontSize: 10, color: "var(--text-muted)" }}>{ccDetails.db_size_mb} MB</Text>
              )}
            </Card>
          </Col>
          <Col span={8}>
            <Card size="small" bordered={false} style={{ background: trafficStatus?.available ? "#f0fdf4" : "var(--bg-secondary)", borderRadius: 8 }}>
              <Statistic
                title={<Text style={{ fontSize: 11, color: "var(--text-secondary)" }}>{lang === "zh" ? "流量数据" : "Traffic Data"}</Text>}
                value={trafficStatus?.total_records ?? 0}
                valueStyle={{ fontSize: 16, fontWeight: 600, color: "var(--accent)" }}
                suffix={<Text style={{ fontSize: 10, color: "var(--text-muted)" }}>{lang === "zh" ? "条记录" : "records"}</Text>}
              />
              {trafficStatus?.last_request && (
                <Text style={{ fontSize: 10, color: "var(--text-muted)" }}>{trafficStatus.last_request}</Text>
              )}
            </Card>
          </Col>
        </Row>
      </Card>

      {/* ── CC Switch Proxy ──────────────────────────────────────── */}
      <Card
        bordered={false}
        style={{ marginBottom: 16, background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}
        bodyStyle={{ padding: "16px 20px" }}
      >
        <Text strong style={{ color: "var(--text-primary)", fontSize: 14 }}>
          <ThunderboltOutlined style={{ marginRight: 6 }} />
          {lang === "zh" ? "CC Switch 代理" : "CC Switch Proxy"}
        </Text>

        <div style={{ marginTop: 12 }}>
          {ccDetails?.available ? (
            <>
              <Descriptions size="small" column={1} colon={false} style={{ marginBottom: 12 }}>
                <Descriptions.Item label={<Text style={{ color: "var(--text-muted)", fontSize: 11 }}>{lang === "zh" ? "数据库路径" : "DB Path"}</Text>}>
                  <Text code style={{ fontSize: 11 }}>{ccDetails.path}</Text>
                </Descriptions.Item>
                {ccDetails.exe_exists && (
                  <Descriptions.Item label={<Text style={{ color: "var(--text-muted)", fontSize: 11 }}>{lang === "zh" ? "程序路径" : "EXE Path"}</Text>}>
                    <Text code style={{ fontSize: 11 }}>{ccDetails.exe_path}</Text>
                  </Descriptions.Item>
                )}
                {ccStatus?.current_provider && (
                  <Descriptions.Item label={<Text style={{ color: "var(--text-muted)", fontSize: 11 }}>{lang === "zh" ? "当前供应商" : "Current Provider"}</Text>}>
                    <Tag color="blue" style={{ fontSize: 11 }}>{ccStatus.current_provider}</Tag>
                  </Descriptions.Item>
                )}
              </Descriptions>

              <Divider style={{ margin: "8px 0", borderColor: "var(--border-light)" }} />

              {ccStatus?.proxy_configured ? (
                <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
                  <Tag color="success" style={{ fontSize: 11 }}>{lang === "zh" ? "已配置" : "Configured"}</Tag>
                  <Text style={{ fontSize: 11, color: "var(--text-secondary)" }}>{ccStatus.proxy_base_url}</Text>
                  <Button icon={<RollbackOutlined />} loading={reverting} onClick={handleRevert} danger size="small">
                    {lang === "zh" ? "撤回配置" : "Revert"}
                  </Button>
                </div>
              ) : (
                <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
                  <Tag color="default" style={{ fontSize: 11 }}>{lang === "zh" ? "未配置" : "Not configured"}</Tag>
                  <Button
                    type="primary" size="small" icon={<ThunderboltOutlined />}
                    loading={applyLoading} onClick={handleApplySetup}
                  >
                    {lang === "zh" ? "一键配置" : "One-Click Setup"}
                  </Button>
                  <Text style={{ fontSize: 10, color: "var(--text-muted)" }}>
                    {lang === "zh"
                      ? "在 CC Switch 中创建 RAgent Proxy 供应商"
                      : "Creates RAgent Proxy provider in CC Switch"}
                  </Text>
                </div>
              )}
            </>
          ) : (
            <Text type="secondary" style={{ fontSize: 12 }}>
              {lang === "zh"
                ? "未检测到 CC Switch。请先安装 CC Switch 并确保数据库文件存在。"
                : "CC Switch not detected. Please install CC Switch first."}
            </Text>
          )}
        </div>
      </Card>

      {/* ── Appearance ───────────────────────────────────────────── */}
      <Card
        bordered={false}
        style={{ marginBottom: 16, background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}
        bodyStyle={{ padding: "16px 20px" }}
      >
        <Text strong style={{ color: "var(--text-primary)", fontSize: 14 }}>
          <BulbOutlined style={{ marginRight: 6 }} />
          {lang === "zh" ? "外观" : "Appearance"}
        </Text>

        <div style={{ marginTop: 12 }}>
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "8px 0" }}>
            <div>
              <Text style={{ color: "var(--text-primary)", fontSize: 13 }}>
                {lang === "zh" ? "主题模式" : "Theme Mode"}
              </Text>
              <br />
              <Text style={{ color: "var(--text-muted)", fontSize: 11 }}>
                {lang === "zh" ? "切换浅色/深色界面风格" : "Switch between light and dark UI"}
              </Text>
            </div>
            <Segmented
              size="small"
              value={themeMode}
              onChange={(v) => setMode(v as "light" | "dark")}
              options={[
                { label: lang === "zh" ? "☀️ 浅色" : "☀️ Light", value: "light" },
                { label: lang === "zh" ? "🌙 深色" : "🌙 Dark", value: "dark" },
              ]}
            />
          </div>

          <Divider style={{ margin: "8px 0", borderColor: "var(--border-light)" }} />

          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "8px 0" }}>
            <div>
              <Text style={{ color: "var(--text-primary)", fontSize: 13 }}>
                <GlobalOutlined style={{ marginRight: 6 }} />
                {lang === "zh" ? "界面语言" : "Language"}
              </Text>
              <br />
              <Text style={{ color: "var(--text-muted)", fontSize: 11 }}>
                {lang === "zh" ? "选择界面显示语言" : "Select interface language"}
              </Text>
            </div>
            <Segmented
              size="small"
              value={i18n.language.startsWith("zh") ? "zh" : "en"}
              onChange={(v) => i18n.changeLanguage(v as string)}
              options={[
                { label: "🇨🇳 中文", value: "zh" },
                { label: "🇺🇸 English", value: "en" },
              ]}
            />
          </div>
        </div>
      </Card>

      {/* ── Backend ──────────────────────────────────────────────── */}
      <Card
        bordered={false}
        style={{ marginBottom: 16, background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}
        bodyStyle={{ padding: "16px 20px" }}
      >
        <Text strong style={{ color: "var(--text-primary)", fontSize: 14 }}>
          <SettingOutlined style={{ marginRight: 6 }} />
          {t("settings.backend")}
        </Text>

        <div style={{ marginTop: 12 }}>
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "6px 0" }}>
            <div>
              <Text style={{ color: "var(--text-primary)", fontSize: 13 }}>{t("settings.backendPort")}</Text>
              <br />
              <Text style={{ color: "var(--text-muted)", fontSize: 11 }}>
                {lang === "zh" ? "后端 API 服务监听端口" : "Backend API server port"}
              </Text>
            </div>
            <Space>
              <Text code style={{ fontSize: 13 }}>:{backendPort}</Text>
              <Button size="small" onClick={() => setBackendPort(backendPort === 15722 ? 15723 : 15722)}>
                {backendPort === 15722 ? "→ 15723" : "→ 15722"}
              </Button>
            </Space>
          </div>

          <Divider style={{ margin: "8px 0", borderColor: "var(--border-light)" }} />

          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "6px 0" }}>
            <div>
              <Text style={{ color: "var(--text-primary)", fontSize: 13 }}>{t("settings.autoStart")}</Text>
              <br />
              <Text style={{ color: "var(--text-muted)", fontSize: 11 }}>
                {lang === "zh" ? "应用启动时自动运行后端服务" : "Auto-start backend on app launch"}
              </Text>
            </div>
            <Switch size="small" checked={autoStartBackend} onChange={(v) => setAutoStartBackend(v)} />
          </div>

          <Divider style={{ margin: "8px 0", borderColor: "var(--border-light)" }} />

          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "6px 0" }}>
            <div>
              <Text style={{ color: "var(--text-primary)", fontSize: 13 }}>{t("settings.closeToTray")}</Text>
              <br />
              <Text style={{ color: "var(--text-muted)", fontSize: 11 }}>
                {lang === "zh" ? "关闭窗口时最小化到系统托盘" : "Minimize to tray on close"}
              </Text>
            </div>
            <Switch size="small" checked={closeToTray} onChange={(v) => setCloseToTray(v)} />
          </div>

          <Divider style={{ margin: "8px 0", borderColor: "var(--border-light)" }} />

          {api && (
            <div style={{ display: "flex", alignItems: "center", gap: 12, paddingTop: 4 }}>
              <Button
                type="primary" size="small" icon={<ReloadOutlined />}
                loading={saving}
                onClick={saveElectronSettings}
              >
                {t("settings.saveSettings")}
              </Button>
              <Button icon={<ReloadOutlined />} size="small" onClick={async () => {
                await api.restartBackend();
                message.info(t("settings.restarting"));
              }}>
                {t("settings.restartBackend")}
              </Button>
            </div>
          )}
        </div>
      </Card>

      {/* ── Data & Logs ──────────────────────────────────────────── */}
      {appInfo && (
        <Card
          bordered={false}
          style={{ marginBottom: 16, background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}
          bodyStyle={{ padding: "16px 20px" }}
        >
          <Text strong style={{ color: "var(--text-primary)", fontSize: 14 }}>
            <DatabaseOutlined style={{ marginRight: 6 }} />
            {lang === "zh" ? "数据与日志" : "Data & Logs"}
          </Text>

          <div style={{ marginTop: 12 }}>
            <Descriptions size="small" column={1} colon={false}>
              <Descriptions.Item label={<Text style={{ color: "var(--text-muted)", fontSize: 11 }}>{lang === "zh" ? "应用数据" : "App Data"}</Text>}>
                <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                  <Text code style={{ fontSize: 11 }}>{appInfo.dataPath}</Text>
                  <Button
                    type="link" size="small" icon={<FolderOpenOutlined />}
                    style={{ fontSize: 11, padding: 0 }}
                    onClick={() => {
                      if (api) api.showItemInFolder(appInfo.dataPath);
                    }}
                  >
                    {lang === "zh" ? "打开" : "Open"}
                  </Button>
                </div>
              </Descriptions.Item>
              <Descriptions.Item label={<Text style={{ color: "var(--text-muted)", fontSize: 11 }}>{lang === "zh" ? "后端日志" : "Backend Logs"}</Text>}>
                <Text code style={{ fontSize: 11 }}>backend/logs/ragent.log</Text>
              </Descriptions.Item>
              <Descriptions.Item label={<Text style={{ color: "var(--text-muted)", fontSize: 11 }}>{lang === "zh" ? "路由日志" : "Route Logs"}</Text>}>
                <Text code style={{ fontSize: 11 }}>backend/logs/routes.log</Text>
              </Descriptions.Item>
              <Descriptions.Item label={<Text style={{ color: "var(--text-muted)", fontSize: 11 }}>{lang === "zh" ? "请求日志" : "Request Logs"}</Text>}>
                <Text code style={{ fontSize: 11 }}>backend/logs/requests.log</Text>
              </Descriptions.Item>
              <Descriptions.Item label={<Text style={{ color: "var(--text-muted)", fontSize: 11 }}>{lang === "zh" ? "应用数据库" : "App Database"}</Text>}>
                <Text code style={{ fontSize: 11 }}>backend/ragent_router.db</Text>
              </Descriptions.Item>
            </Descriptions>
          </div>
        </Card>
      )}

      {/* ── About ────────────────────────────────────────────────── */}
      {appInfo && (
        <Card
          bordered={false}
          style={{ background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}
          bodyStyle={{ padding: "16px 20px" }}
        >
          <Text strong style={{ color: "var(--text-primary)", fontSize: 14 }}>
            <InfoCircleOutlined style={{ marginRight: 6 }} />
            {t("settings.about")}
          </Text>

          <div style={{ marginTop: 12 }}>
            <Descriptions size="small" column={2} colon={false}>
              <Descriptions.Item label={<Text style={{ color: "var(--text-muted)", fontSize: 11 }}>{t("settings.version")}</Text>}>
                <Tag color="purple" style={{ fontSize: 11 }}>{appInfo.version}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label={<Text style={{ color: "var(--text-muted)", fontSize: 11 }}>{t("settings.platform")}</Text>}>
                <Text style={{ color: "var(--text-primary)", fontSize: 12 }}>{appInfo.platform} ({appInfo.arch})</Text>
              </Descriptions.Item>
              <Descriptions.Item label={<Text style={{ color: "var(--text-muted)", fontSize: 11 }}>{t("settings.electron")}</Text>}>
                <Text style={{ color: "var(--text-primary)", fontSize: 12 }}>{appInfo.electronVersion}</Text>
              </Descriptions.Item>
              <Descriptions.Item label={<Text style={{ color: "var(--text-muted)", fontSize: 11 }}>{t("settings.nodejs")}</Text>}>
                <Text style={{ color: "var(--text-primary)", fontSize: 12 }}>{appInfo.nodeVersion}</Text>
              </Descriptions.Item>
              <Descriptions.Item label={<Text style={{ color: "var(--text-muted)", fontSize: 11 }}>{lang === "zh" ? "开发模式" : "Dev Mode"}</Text>}>
                <Tag color={appInfo.isDev ? "orange" : "green"} style={{ fontSize: 11 }}>
                  {appInfo.isDev ? (lang === "zh" ? "开发" : "Dev") : (lang === "zh" ? "生产" : "Production")}
                </Tag>
              </Descriptions.Item>
            </Descriptions>
          </div>
        </Card>
      )}
    </div>
  );
}
