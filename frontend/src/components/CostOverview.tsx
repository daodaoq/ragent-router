import { Card, Row, Col, Statistic } from "antd";
import { useTranslation } from "react-i18next";
import type { CostOverview as CostOverviewType } from "../api";

interface Props {
  overview: CostOverviewType;
}

export default function CostOverview({ overview }: Props) {
  const { t } = useTranslation();

  const cards = [
    {
      title: t("dashboard.todayCost"),
      value: overview.today_cost,
      prefix: "$",
      color: "var(--accent)",
      precision: 2,
    },
    {
      title: t("dashboard.monthCost"),
      value: overview.month_cost,
      prefix: "$",
      color: "var(--green)",
      precision: 2,
    },
    {
      title: t("dashboard.saved"),
      value: overview.saved_amount,
      prefix: "$",
      color: "#06b6d4",
      precision: 2,
    },
    {
      title: t("dashboard.savingRate"),
      value: overview.saving_rate,
      suffix: "%",
      color: "var(--orange)",
      precision: 1,
    },
  ];

  return (
    <Row gutter={[16, 16]}>
      {cards.map((card) => (
        <Col xs={24} sm={12} lg={6} key={card.title}>
          <Card
            bordered={false}
            style={{
              background: "var(--bg-card)",
              border: "1px solid var(--border-light)",
              borderRadius: 10,
              boxShadow: "0 1px 2px rgba(0,0,0,0.04)",
            }}
          >
            <Statistic
              title={<span style={{ color: "var(--text-secondary)", fontSize: 12, fontWeight: 500 }}>{card.title}</span>}
              value={card.value}
              precision={card.precision}
              suffix={card.suffix}
              valueStyle={{ color: card.color, fontSize: 28, fontWeight: 700 }}
              prefix={<span style={{ color: card.color, fontSize: 22 }}>{card.prefix}</span>}
            />
          </Card>
        </Col>
      ))}
      <Col xs={24} sm={12} lg={6}>
        <Card
          bordered={false}
          style={{
            background: "var(--bg-card)",
            border: "1px solid var(--border-light)",
            borderRadius: 10,
            boxShadow: "0 1px 2px rgba(0,0,0,0.04)",
          }}
        >
          <Statistic
            title={<span style={{ color: "var(--text-secondary)", fontSize: 12, fontWeight: 500 }}>{t("dashboard.totalRequests")}</span>}
            value={overview.total_requests}
            valueStyle={{ color: "var(--text-primary)", fontSize: 28, fontWeight: 700 }}
          />
        </Card>
      </Col>
    </Row>
  );
}
