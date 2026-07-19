// Setup API —— 首次配置引导
import { request } from "./client";

export interface SetupStatus {
  ccswitch_available: boolean;
  proxy_configured: boolean;
  current_provider: string | null;
  proxy_base_url: string;
}

export interface SetupResult {
  success: boolean;
  message?: string;
}

export const setupApi = {
  getStatus: () => request<SetupStatus>("/api/setup/status"),
  apply: () => request<SetupResult>("/api/setup/apply", { method: "POST" }),
  revert: () => request<SetupResult>("/api/setup/revert", { method: "POST" }),
};
