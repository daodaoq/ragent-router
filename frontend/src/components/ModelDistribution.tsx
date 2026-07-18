import { Card, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { PieChart, Pie, Cell, Tooltip, Legend, ResponsiveContainer } from "recharts";
import { useDashboardStore } from "../stores/dashboard";

const { Text } = Typography;

const COLORS: Record<string, string> = {
  "claude-sonnet-4-6": "var(--accent)",
  "deepseek-chat": "var(--green)",
  "gpt-4o": "#06b6d4",
};

export default function ModelDistribution() {
  const { t } = useTranslation();
  const { modelDistribution } = useDashboardStore();

  const data = modelDistribution.map((item) => ({
    name: item.model,
    value: item.count,
    color: COLORS[item.model] || COLORS[item.model.split("-")[0]] || "var(--text-muted)",
  }));

  return (
    <Card
      title={<span style={{ color: "var(--text-primary)", fontSize: 14, fontWeight: 600 }}>{t("dashboard.modelDistribution")}</span>}
      bordered={false}
      style={{
        background: "var(--bg-card)",
        border: "1px solid var(--border-light)",
        borderRadius: 10,
        height: "100%",
      }}
      bodyStyle={{ paddingBottom: 8 }}
    >
      {data.length === 0 ? (
        <Text type="secondary">{t("dashboard.noDataYet")}</Text>
      ) : (
        <ResponsiveContainer width="100%" height={260}>
          <PieChart>
            <Pie data={data} cx="50%" cy="50%" innerRadius={55} outerRadius={90} paddingAngle={4} dataKey="value" stroke="none">
              {data.map((entry, i) => (
                <Cell key={i} fill={entry.color} />
              ))}
            </Pie>
            <Tooltip
              contentStyle={{
                background: "var(--bg-card)",
                border: "1px solid var(--border-light)",
                borderRadius: 8,
                color: "var(--text-primary)",
              }}
              formatter={(value: number, name: string) => [`${value} ${t("dashboard.requests")}`, name]}
            />
            <Legend
              wrapperStyle={{ color: "var(--text-secondary)", fontSize: 12 }}
              formatter={(value: string) => {
                const pct = modelDistribution.find((d) => d.model === value);
                return `${value} (${pct?.percentage ?? 0}%)`;
              }}
            />
          </PieChart>
        </ResponsiveContainer>
      )}
    </Card>
  );
}
