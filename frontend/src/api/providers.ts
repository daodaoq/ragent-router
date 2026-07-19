// Providers API —— CC Switch 供应商列表与状态
import { request } from "./client";

export interface ProviderItem {
  id: string;
  name: string;
  app_type: string;
  category: string;
  is_current: boolean;
  icon_color: string;
  enabled: boolean;
  endpoints: { app_type: string; url: string }[];
}

export interface ProvidersResponse {
  items: ProviderItem[];
  total: number;
}

export interface CCStatus {
  available: boolean;
  path: string;
  db_size_mb: number;
}

export interface ActivateResult {
  success: boolean;
  provider_name: string;
  message: string;
}

export const providersApi = {
  /** 获取供应商列表 */
  list: () => request<ProvidersResponse>("/api/ccswitch/providers"),

  /** 获取 CC Switch 数据库状态 */
  getStatus: () => request<CCStatus>("/api/ccswitch/status"),

  /** 锁定到指定供应商（调试模式） */
  activate: (providerId: string) =>
    request<ActivateResult>(`/api/proxy/activate/${providerId}`, { method: "POST" }),

  /** 清除调试锁定 */
  deactivate: () =>
    request<ActivateResult>("/api/proxy/activate/_clear", { method: "DELETE" }),
};
