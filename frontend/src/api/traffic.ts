// Traffic API —— 流量监控数据
import { request } from "./client";

export interface TrafficOverview {
  available: boolean;
  today?: { requests: number; tokens: number; cost_usd: number };
  total?: { requests: number; tokens: number; cost_usd: number };
  cache?: { hit_rate: number; total_hit: number };
  error?: { count: number; rate: number };
}

export interface TrafficByModelItem {
  model: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cost_usd: number;
  avg_latency_ms: number;
  min_latency_ms: number;
  max_latency_ms: number;
}

export interface TrafficRecentItem {
  id: string;
  created_at: string;
  model: string;
  provider: string;
  prompt: string;
  total_tokens: number;
  cost_usd: number;
  latency_ms: number;
  status: string;
}

export interface TrafficTrendPoint {
  date: string;
  cost: number;
  requests: number;
}

export interface TrafficErrorItem {
  count: number;
  rate: number;
}

export interface TrafficLatencyItem {
  model: string;
  avg_ms: number;
  min_ms: number;
  max_ms: number;
}

export interface TrafficStatus {
  available: boolean;
  db_ok: boolean;
}

export const trafficApi = {
  getOverview: () => request<TrafficOverview>("/api/traffic/overview"),
  getByModel: () => request<{ items: TrafficByModelItem[] }>("/api/traffic/by-model"),
  getRecent: (limit = 50) =>
    request<{ items: TrafficRecentItem[] }>(`/api/traffic/recent?limit=${limit}`),
  getDailyTrend: (days = 14) =>
    request<{ points: TrafficTrendPoint[] }>(`/api/traffic/daily-trend?days=${days}`),
  getErrors: (hours = 24) =>
    request<TrafficErrorItem>(`/api/traffic/errors?hours=${hours}`),
  getLatency: (hours = 24) =>
    request<{ items: TrafficLatencyItem[] }>(`/api/traffic/latency?hours=${hours}`),
  getStatus: () => request<TrafficStatus>("/api/traffic/status"),
};
