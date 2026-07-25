// Dashboard API —— 仪表盘聚合数据（适配 v0.3.0 新 API）
import { request } from "./client";

export interface CostOverview {
  today_cost: number;
  month_cost: number;
  total_cost: number;
  today_requests: number;
  total_requests: number;
  saved_cost: number;
}

export interface ModelDistributionItem {
  model: string;
  count: number;
  percentage: number;
}

export interface RecentLogItem {
  id: number;
  prompt: string;
  model: string;
  provider: string;
  route_reason: string;
  cost_usd: number;
  latency_ms: number;
  status: string;
  created_at: string;
}

export interface CostTrendPoint {
  date: string;
  cost: number;
  requests: number;
}

export interface MonitorOverview {
  today_requests: number;
  error_count: number;
  total_tokens: number;
  avg_latency_ms: number;
}

export interface ByModelData {
  model: string;
  count: number;
  total_tokens: number;
  avg_latency_ms: number;
  total_cost: number;
}

export const dashboardApi = {
  // 新 API（需要 JWT 认证）
  getOverview: () => request<{ success: boolean; data: CostOverview }>("/api/dashboard/overview"),
  getModelDistribution: () =>
    request<{ success: boolean; data: ModelDistributionItem[] }>("/api/dashboard/model-distribution"),
  getRecentLogs: (limit = 20) =>
    request<{ success: boolean; data: RecentLogItem[] }>(`/api/dashboard/recent-logs?limit=${limit}`),
  getCostTrend: (days = 7) =>
    request<{ success: boolean; data: CostTrendPoint[] }>(`/api/dashboard/cost-trend?days=${days}`),
  getMonitorOverview: () =>
    request<{ success: boolean; data: MonitorOverview }>("/api/monitor/overview"),
  getByModel: () =>
    request<{ success: boolean; data: ByModelData[] }>("/api/monitor/by-model"),

  // 旧 API（兼容）
  getLegacyOverview: () => request<CostOverview>("/api/legacy/dashboard/overview"),
};
