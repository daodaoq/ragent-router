// Monitor API —— 观测台面板数据
import { request } from "./client";

export interface MonitorOverview {
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

export interface MonitorLogItem {
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

export const monitorApi = {
  getOverview: () => request<MonitorOverview>("/api/monitor/overview"),
  getRecent: (limit = 100) =>
    request<{ items: MonitorLogItem[] }>(`/api/monitor/recent?limit=${limit}`),
};
