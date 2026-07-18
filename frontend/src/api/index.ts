const BASE_URL = "http://localhost:15722";

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE_URL}${url}`, {
    headers: { "Content-Type": "application/json" },
    ...options,
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`API error ${res.status}: ${text}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

// ── Dashboard ─────────────────────────────────────────────────────

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

// ── Proxy (instant provider switching) ─────────────────────────────

export interface ProxyCurrent {
  provider_id: string;
  provider_name: string;
  endpoints: { app_type: string; url: string }[];
  is_valid: boolean;
  base_url: string | null;
  warning: string | null;
}

export interface ProxyHealth {
  ccswitch_db_available: boolean;
  state_file_ok: boolean;
  active_provider_id: string;
  active_provider_valid: boolean;
  warnings: string[];
  proxy_ready: boolean;
}

export const proxyApi = {
  getCurrent: () => request<ProxyCurrent>("/api/proxy/current"),
  activate: (providerId: string) =>
    request<{ success: boolean; provider_name: string; message: string }>(
      `/api/proxy/activate/${providerId}`,
      { method: "POST" }
    ),
  getHealth: () => request<ProxyHealth>("/api/proxy/health"),
};
