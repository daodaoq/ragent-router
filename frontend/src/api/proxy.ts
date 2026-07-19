// Proxy API —— 供应商管理与调试锁定
import { request } from "./client";

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

export interface ActivateResult {
  success: boolean;
  provider_name: string;
  message: string;
}

export const proxyApi = {
  getCurrent: () => request<ProxyCurrent>("/api/proxy/current"),
  activate: (providerId: string) =>
    request<ActivateResult>(`/api/proxy/activate/${providerId}`, { method: "POST" }),
  deactivate: () =>
    request<ActivateResult>(`/api/proxy/activate/_clear`, { method: "DELETE" }),
  getHealth: () => request<ProxyHealth>("/api/proxy/health"),
};
