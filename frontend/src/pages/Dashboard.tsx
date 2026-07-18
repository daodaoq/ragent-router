import { useEffect } from "react";
import { Row, Col, Spin, Empty, Button } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useDashboardStore } from "../stores/dashboard";
import CostOverview from "../components/CostOverview";
import ModelDistribution from "../components/ModelDistribution";
import RecentRoutes from "../components/RecentRoutes";
import CostTrend from "../components/CostTrend";
import PageHelp from "../components/PageHelp";

export default function Dashboard() {
  const { t } = useTranslation();
  const { overview, loading, fetchAll } = useDashboardStore();

  useEffect(() => {
    fetchAll();
  }, [fetchAll]);

  if (loading && !overview) {
    return (
      <div style={{ textAlign: "center", paddingTop: 120 }}>
        <Spin size="large" tip={t("dashboard.loading")} />
      </div>
    );
  }

  if (!overview) {
    return (
      <Empty description={t("dashboard.noData")} style={{ paddingTop: 120 }}>
        <Button type="primary" icon={<ReloadOutlined />} onClick={fetchAll}>
          {t("dashboard.retry")}
        </Button>
      </Empty>
    );
  }

  return (
    <div style={{ padding: 20 }}>
      <div style={{ marginBottom: 16, fontSize: 16, fontWeight: 600, color: "var(--text-primary)" }}>
        📊 {t("dashboard.title")}
        <PageHelp page="dashboard" />
      </div>
      <CostOverview overview={overview} />
      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} lg={10}>
          <ModelDistribution />
        </Col>
        <Col xs={24} lg={14}>
          <RecentRoutes />
        </Col>
      </Row>
      <Row style={{ marginTop: 16 }}>
        <Col span={24}>
          <CostTrend />
        </Col>
      </Row>
    </div>
  );
}
