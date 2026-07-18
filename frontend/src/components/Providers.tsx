import { useEffect, useState } from "react";
import { Card, Tag, Typography, Spin, Empty, Row, Col, Tooltip, Button, message, Alert } from "antd";
import {
  ApiOutlined, LinkOutlined, CheckCircleFilled, ThunderboltOutlined,
  ExclamationCircleOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import PageHelp from "./PageHelp";

const { Text, Title } = Typography;

interface Endpoint { app_type: string; url: string; }

interface Provider {
  id: string; app_type: string; name: string; category: string;
  is_current: boolean; endpoints: Endpoint[];
  cost_multiplier: string; icon_color: string;
  limit_daily_usd: string | null; limit_monthly_usd: string | null;
}

const CATEGORY_LABELS: Record<string, { en: string; zh: string; color: string }> = {
  official: { en: "Official", zh: "官方", color: "blue" },
  cn_official: { en: "CN Official", zh: "国内官方", color: "orange" },
  custom: { en: "Custom", zh: "自定义", color: "default" },
};

const APP_TYPE_LABELS: Record<string, string> = {
  claude: "Claude", codex: "OpenAI", gemini: "Gemini",
};

export default function Providers() {
  const { t, i18n } = useTranslation();
  const lang = i18n.language.startsWith("zh") ? "zh" : "en";
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [dbStatus, setDbStatus] = useState<{ available: boolean; path: string; db_size_mb: number } | null>(null);
  const [activeId, setActiveId] = useState<string>("");
  const [activating, setActivating] = useState<string | null>(null);
  const fetchProviders = () => {
    fetch("http://localhost:15722/api/ccswitch/providers")
      .then(r => r.json())
      .then(data => {
        const items = (data.items || []).filter((p: Provider) => p.name !== "default");
        setProviders(items);
        // Find current provider from is_current flag
        const current = items.find((p: Provider) => p.is_current);
        if (current) setActiveId(current.id);
        setLoading(false);
      }).catch(() => setLoading(false));
  };

  useEffect(() => {
    fetchProviders();
    fetch("http://localhost:15722/api/ccswitch/status")
      .then(r => r.json()).then(setDbStatus).catch(() => {});
  }, []);

  const handleActivate = async (provider: Provider) => {
    if (provider.id === activeId) return;
    setActivating(provider.id);
    try {
      const res = await fetch(`http://localhost:15722/api/ccswitch/activate/${provider.id}`, { method: "POST" });
      const data = await res.json();
      if (data.success) {
        setActiveId(provider.id);
        message.success(
          lang === "zh"
            ? `已切换到 ${data.provider_name} — Claude Code 下个问题自动用新供应商`
            : `Switched to ${data.provider_name} — next Claude Code request uses new provider`
        );
      } else {
        message.error(data.detail || data.message || "Failed");
      }
    } catch {
      message.error(lang === "zh" ? "切换失败" : "Activation failed");
    }
    setActivating(null);
  };

  if (loading) return <div style={{ textAlign: "center", paddingTop: 120 }}><Spin size="large" /></div>;
  if (!dbStatus?.available) return <Empty description={t("providers.notFound")} style={{ paddingTop: 120 }} />;

  return (
    <div style={{ padding: 20 }}>
      <div style={{ marginBottom: 20 }}>
        <Title level={4} style={{ color: "var(--text-primary)", marginBottom: 4 }}>
          <ApiOutlined style={{ marginRight: 8 }} />
          {lang === "zh" ? "API 供应商" : "API Providers"}
          <PageHelp page="providers" />
        </Title>
        <Text type="secondary" style={{ fontSize: 12 }}>
          {lang === "zh" ? "数据来源: CC Switch 本地数据库" : "Data source: CC Switch local database"}
        </Text>

        <Alert
          type="success"
          icon={<ThunderboltOutlined />}
          message={lang === "zh"
            ? "点击「启用」直接切换 Claude Code 供应商 — 下个问题自动生效，无需重启。"
            : "Click 'Activate' to switch Claude Code provider — takes effect on the next question, no restart."}
          style={{ marginTop: 10, fontSize: 12 }}
        />
      </div>

      <Row gutter={[16, 16]}>
        {providers.map(p => {
          const cat = CATEGORY_LABELS[p.category] || { en: p.category, zh: p.category, color: "default" };
          const appLabel = APP_TYPE_LABELS[p.app_type] || p.app_type;
          const color = p.icon_color || "var(--accent)";
          const isActive = p.id === activeId;

          return (
            <Col xs={24} sm={12} lg={8} key={p.id}>
              <Card
                bordered={false}
                style={{
                  background: "var(--bg-card)",
                  border: isActive ? "2px solid #6366f1" : "1px solid #e5e7eb",
                  borderRadius: 10,
                  height: "100%",
                }}
                bodyStyle={{ padding: "20px 20px 16px" }}
              >
                {/* Header */}
                <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 14 }}>
                  <div style={{
                    width: 36, height: 36, borderRadius: 8, background: color,
                    display: "flex", alignItems: "center", justifyContent: "center",
                    color: "#fff", fontSize: 16, fontWeight: 700, flexShrink: 0,
                  }}>{p.name.charAt(0)}</div>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <Text strong style={{ color: "var(--text-primary)", fontSize: 14 }}>{p.name}</Text>
                    <div style={{ display: "flex", gap: 6, marginTop: 4 }}>
                      <Tag style={{ fontSize: 10, lineHeight: "16px" }}>{appLabel}</Tag>
                      <Tag color={cat.color} style={{ fontSize: 10, lineHeight: "16px" }}>{cat[lang]}</Tag>
                    </div>
                  </div>
                </div>

                {/* Endpoints */}
                {p.endpoints.length > 0 && (
                  <div style={{ background: "var(--bg-secondary)", borderRadius: 6, padding: "8px 10px", marginBottom: 12 }}>
                    {p.endpoints.map((ep, i) => (
                      <div key={i} style={{ display: "flex", alignItems: "center", gap: 6, padding: "3px 0", fontSize: 11 }}>
                        <LinkOutlined style={{ color: "var(--text-muted)", fontSize: 10 }} />
                        <Text style={{ color: "var(--text-secondary)", fontSize: 11, fontFamily: "monospace" }} ellipsis>{ep.url}</Text>
                      </div>
                    ))}
                  </div>
                )}

                {/* Footer */}
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-end", flexWrap: "wrap", gap: 8 }}>
                  <div style={{ fontSize: 11, color: "var(--text-muted)" }}>
                    <span>{lang === "zh" ? "倍率" : "Multiplier"}: {p.cost_multiplier || "1.0"}x</span>
                  </div>

                  {isActive ? (
                    <Tag color="success" style={{ fontSize: 11, margin: 0 }}>
                      <CheckCircleFilled style={{ marginRight: 4 }} />
                      {lang === "zh" ? "当前使用中" : "Active"}
                    </Tag>
                  ) : (
                    <Button
                      type="primary" size="small"
                      icon={<ThunderboltOutlined />}
                      loading={activating === p.id}
                      onClick={() => handleActivate(p)}
                      style={{ fontSize: 11 }}
                    >
                      {lang === "zh" ? "启用" : "Activate"}
                    </Button>
                  )}
                </div>
              </Card>
            </Col>
          );
        })}
      </Row>
    </div>
  );
}
