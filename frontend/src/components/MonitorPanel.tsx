import { useEffect, useState } from "react";
import { Card, Table, Tag, Typography, Spin, Empty, Statistic, Row, Col, Space } from "antd";
import {
  MonitorOutlined, ApiOutlined, ClockCircleOutlined,
  ThunderboltOutlined, CheckCircleOutlined, CloseCircleOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";

const { Text, Title } = Typography;

const API = "http://localhost:15722";

interface LogItem {
  id: string;
  created_at: string;
  prompt: string;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  model: string;
  provider: string;
  route_reason: string;
  intent_match: string;
  intent_score: number;
  status: string;
  cost_usd: number;
  latency_ms: number;
}

interface Overview {
  total_requests: number;
  today_requests: number;
  error_count: number;
  error_rate: number;
  total_tokens: number;
  total_cost_usd: number;
  avg_latency_ms: number;
  by_provider: { provider: string; requests: number; cost_usd: number }[];
  by_model: { model: string; requests: number; cost_usd: number; avg_latency_ms: number }[];
  by_intent: { intent: string; requests: number; cost_usd: number }[];
}

export default function MonitorPanel() {
  const { t, i18n } = useTranslation();
  const lang = i18n.language.startsWith("zh") ? "zh" : "en";

  const [logs, setLogs] = useState<LogItem[]>([]);
  const [overview, setOverview] = useState<Overview | null>(null);
  const [loading, setLoading] = useState(true);

  const fetchData = async () => {
    try {
      const [logRes, ovRes] = await Promise.all([
        fetch(`${API}/api/monitor/recent?limit=100`).then(r => r.json()),
        fetch(`${API}/api/monitor/overview`).then(r => r.json()),
      ]);
      setLogs(logRes.items || []);
      setOverview(ovRes);
    } catch (e) {
      console.error(e);
    }
    setLoading(false);
  };

  useEffect(() => { fetchData(); }, []);

  // Auto-refresh every 10 seconds
  useEffect(() => {
    const interval = setInterval(fetchData, 10_000);
    return () => clearInterval(interval);
  }, []);

  const columns = [
    {
      title: lang === "zh" ? "时间" : "Time", dataIndex: "created_at", width: 140,
      render: (v: string) => <Text style={{ fontSize: 11, color: "var(--text-muted)" }}>{v?.substring(11, 19) || "—"}</Text>,
    },
    {
      title: lang === "zh" ? "供应商" : "Provider", dataIndex: "provider", width: 90,
      render: (v: string) => {
        const colors: Record<string, string> = { DeepSeek: "blue", MiniMax: "red", Bailian: "purple", claude: "purple", deepseek: "green" };
        return <Tag color={colors[v] || "default"} style={{ fontSize: 10 }}>{v}</Tag>;
      },
    },
    {
      title: lang === "zh" ? "模型" : "Model", dataIndex: "model", width: 140,
      render: (v: string) => <Text style={{ fontSize: 11, color: "var(--text-primary)" }}>{v}</Text>,
    },
    {
      title: lang === "zh" ? "问题" : "Question", dataIndex: "prompt", ellipsis: true,
      render: (v: string) => (
        <Text style={{ fontSize: 11, color: "var(--text-secondary)", maxWidth: 200, display: "inline-block", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {v || "—"}
        </Text>
      ),
    },
    {
      title: lang === "zh" ? "Token" : "Tokens", dataIndex: "total_tokens", width: 70, align: "right" as const,
      render: (v: number) => <Text style={{ fontSize: 11, color: "var(--accent)" }}>{v}</Text>,
    },
    {
      title: lang === "zh" ? "费用" : "Cost", dataIndex: "cost_usd", width: 70, align: "right" as const,
      render: (v: number) => <Text style={{ fontSize: 11, color: v > 0.001 ? "var(--orange)" : "var(--text-muted)" }}>${v.toFixed(4)}</Text>,
    },
    {
      title: lang === "zh" ? "延迟" : "Latency", dataIndex: "latency_ms", width: 70, align: "right" as const,
      render: (v: number) => <Text style={{ fontSize: 11, color: v > 5000 ? "var(--red)" : v > 2000 ? "var(--orange)" : "var(--green)" }}>{v}ms</Text>,
    },
    {
      title: lang === "zh" ? "状态" : "Status", dataIndex: "status", width: 60,
      render: (v: string) => v === "ok"
        ? <CheckCircleOutlined style={{ color: "var(--green)", fontSize: 14 }} />
        : <CloseCircleOutlined style={{ color: "var(--red)", fontSize: 14 }} />,
    },
  ];

  if (loading && !overview) {
    return <div style={{ textAlign: "center", paddingTop: 120 }}><Spin size="large" /></div>;
  }

  return (
    <div style={{ padding: 20 }}>
      <Title level={4} style={{ color: "var(--text-primary)", marginBottom: 4 }}>
        <MonitorOutlined style={{ marginRight: 8 }} />
        {lang === "zh" ? "观测台" : "Monitor"}
      </Title>
      <Text type="secondary" style={{ fontSize: 12 }}>
        {lang === "zh"
          ? "实时查看每次 Claude Code 请求使用的供应商、模型、Token 消耗和延迟"
          : "Real-time view of every Claude Code request — provider, model, tokens & latency"}
      </Text>

      {/* Overview stats */}
      {overview && (
        <Row gutter={[12, 12]} style={{ marginTop: 16 }}>
          <Col xs={12} sm={6}>
            <Card size="small" bordered={false} style={{ background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}>
              <Statistic
                title={<Text style={{ fontSize: 11, color: "var(--text-muted)" }}>{lang === "zh" ? "总请求" : "Total Requests"}</Text>}
                value={overview.total_requests}
                valueStyle={{ fontSize: 22, fontWeight: 700, color: "var(--accent)" }}
              />
            </Card>
          </Col>
          <Col xs={12} sm={6}>
            <Card size="small" bordered={false} style={{ background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}>
              <Statistic
                title={<Text style={{ fontSize: 11, color: "var(--text-muted)" }}>{lang === "zh" ? "今日" : "Today"}</Text>}
                value={overview.today_requests}
                valueStyle={{ fontSize: 22, fontWeight: 700, color: "var(--accent)" }}
              />
            </Card>
          </Col>
          <Col xs={12} sm={6}>
            <Card size="small" bordered={false} style={{ background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}>
              <Statistic
                title={<Text style={{ fontSize: 11, color: "var(--text-muted)" }}>{lang === "zh" ? "错误率" : "Error Rate"}</Text>}
                value={overview.error_rate}
                suffix="%"
                valueStyle={{ fontSize: 22, fontWeight: 700, color: overview.error_rate > 5 ? "var(--red)" : "var(--green)" }}
              />
            </Card>
          </Col>
          <Col xs={12} sm={6}>
            <Card size="small" bordered={false} style={{ background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}>
              <Statistic
                title={<Text style={{ fontSize: 11, color: "var(--text-muted)" }}>{lang === "zh" ? "平均延迟" : "Avg Latency"}</Text>}
                value={overview.avg_latency_ms}
                suffix="ms"
                valueStyle={{ fontSize: 22, fontWeight: 700, color: "var(--text-primary)" }}
              />
            </Card>
          </Col>
        </Row>
      )}

      {/* Provider/model breakdown */}
      {overview && (
        <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
          <Col xs={24} md={8}>
            <Card
              size="small" bordered={false}
              style={{ background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}
              title={<Text strong style={{ fontSize: 13, color: "var(--text-primary)" }}><ApiOutlined style={{ marginRight: 6 }} />{lang === "zh" ? "供应商分布" : "By Provider"}</Text>}
            >
              {overview.by_provider.map(p => (
                <div key={p.provider} style={{ display: "flex", justifyContent: "space-between", padding: "4px 0", borderBottom: "1px solid var(--border-light)" }}>
                  <Space size={4}>
                    <Tag color={p.provider === "MiniMax" ? "red" : p.provider === "DeepSeek" ? "blue" : p.provider === "Bailian" ? "purple" : "default"} style={{ fontSize: 10 }}>{p.provider}</Tag>
                  </Space>
                  <Space size={8}>
                    <Text style={{ fontSize: 11, color: "var(--text-secondary)" }}>{p.requests} req</Text>
                    <Text style={{ fontSize: 11, color: "var(--text-muted)" }}>${p.cost_usd.toFixed(4)}</Text>
                  </Space>
                </div>
              ))}
            </Card>
          </Col>
          <Col xs={24} md={8}>
            <Card
              size="small" bordered={false}
              style={{ background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}
              title={<Text strong style={{ fontSize: 13, color: "var(--text-primary)" }}><ThunderboltOutlined style={{ marginRight: 6 }} />{lang === "zh" ? "模型分布" : "By Model"}</Text>}
            >
              {overview.by_model.map(m => (
                <div key={m.model} style={{ display: "flex", justifyContent: "space-between", padding: "4px 0", borderBottom: "1px solid var(--border-light)" }}>
                  <Text style={{ fontSize: 11, color: "var(--text-primary)" }}>{m.model}</Text>
                  <Space size={8}>
                    <Text style={{ fontSize: 11, color: "var(--text-secondary)" }}>{m.requests} req</Text>
                    <Text style={{ fontSize: 11, color: "var(--text-muted)" }}>{m.avg_latency_ms}ms</Text>
                  </Space>
                </div>
              ))}
            </Card>
          </Col>
          <Col xs={24} md={8}>
            <Card
              size="small" bordered={false}
              style={{ background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}
              title={<Text strong style={{ fontSize: 13, color: "var(--text-primary)" }}>{lang === "zh" ? "费用总览" : "Cost Summary"}</Text>}
            >
              <Statistic
                title={<Text style={{ fontSize: 11, color: "var(--text-muted)" }}>{lang === "zh" ? "累计费用" : "Total Cost"}</Text>}
                value={overview.total_cost_usd}
                precision={4}
                prefix="$"
                valueStyle={{ fontSize: 24, fontWeight: 700, color: "var(--orange)" }}
              />
              <div style={{ marginTop: 8 }}>
                <Statistic
                  title={<Text style={{ fontSize: 11, color: "var(--text-muted)" }}>{lang === "zh" ? "累计 Token" : "Total Tokens"}</Text>}
                  value={overview.total_tokens}
                  valueStyle={{ fontSize: 18, fontWeight: 600, color: "var(--accent)" }}
                />
              </div>
            </Card>
          </Col>
        </Row>
      )}

      {/* Recent requests table */}
      <Card
        bordered={false}
        style={{ marginTop: 16, background: "var(--bg-card)", border: "1px solid var(--border-light)", borderRadius: 10 }}
        bodyStyle={{ padding: "8px 0" }}
        title={
          <Space>
            <ClockCircleOutlined />
            <Text strong style={{ fontSize: 13, color: "var(--text-primary)" }}>
              {lang === "zh" ? "请求日志" : "Request Log"} ({logs.length})
            </Text>
          </Space>
        }
      >
        {logs.length === 0 ? (
          <Empty description={lang === "zh" ? "暂无请求日志，通过代理发送请求后会自动记录" : "No logs yet — send a request through the proxy"} />
        ) : (
          <Table
            dataSource={logs}
            columns={columns}
            rowKey="id"
            size="small"
            scroll={{ x: 900 }}
            pagination={{ pageSize: 30, size: "small", showSizeChanger: false }}
            locale={{ emptyText: "—" }}
          />
        )}
      </Card>
    </div>
  );
}
