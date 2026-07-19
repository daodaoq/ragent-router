// Dashboard API —— 仪表盘聚合数据
import { request } from "./client";

export interface CostOverview {
  today_cost: number;
  month_cost: number;
  saved_amount: number;
  saving_rate: number;
  total_requests: number;
}

export interface ModelDistributionItem {
  model: string;
  count: number;
  percentage: number;
}

export interface RecentRouteItem {
  id: string;
  prompt: string;
  model: string;
  provider: string;
  route_reason: string;
  cost_usd: number;
  latency_ms: number;
  created_at: string;
}

export interface CostTrendPoint {
  date: string;
  cost: number;
  requests: number;
}

export const dashboardApi = {
  getOverview: () => request<CostOverview>("/api/dashboard/overview"),
  getModelDistribution: () =>
    request<{ items: ModelDistributionItem[] }>("/api/dashboard/model-distribution"),
  getRecentRoutes: (limit = 20) =>
    request<{ items: RecentRouteItem[] }>(`/api/dashboard/recent-routes?limit=${limit}`),
  getCostTrend: (days = 7) =>
    request<{ points: CostTrendPoint[] }>(`/api/dashboard/cost-trend?days=${days}`),
};
