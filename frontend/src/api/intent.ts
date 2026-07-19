// Intent API —— 意图树管理与分类
import { request } from "./client";

// ── 树节点 ─────────────────────────────────────────────────────────

export interface IntentNode {
  id: number;
  intent_code: string;
  parent_code: string | null;
  name: string;
  description: string;
  examples: string[];
  level: number;
  provider_id: string | null;
  enabled: boolean;
  sort_order: number;
  children?: IntentNode[];
}

export interface IntentTreeResponse {
  roots: IntentNode[];
}

// ── 分类器 ─────────────────────────────────────────────────────────

export interface ClassifierConfig {
  configured: boolean;
  provider_name?: string;
  model?: string;
  source?: string;
  message?: string;
}

export interface DefaultProvider {
  found: boolean;
  id?: string;
  name?: string;
}

export interface ClassifyCandidate {
  intent_code: string;
  name: string;
  description: string;
  score: number;
  reason: string;
  provider_id: string;
  provider_name: string;
}

export interface SwitchResult {
  success: boolean;
  provider_name: string;
  detail: string;
  fallback: boolean;
}

export interface ClassifyResult {
  question: string;
  candidates: ClassifyCandidate[];
  top: ClassifyCandidate[];
  matched: ClassifyCandidate | null;
  default_provider: DefaultProvider;
  switched?: SwitchResult;
}

// ── 节点 CRUD ──────────────────────────────────────────────────────

export interface IntentNodeRecord {
  intent_code: string;
  parent_code?: string | null;
  name: string;
  description?: string;
  examples?: string;
  level: number;
  provider_id?: string | null;
  enabled?: boolean;
  sort_order?: number;
}

export const intentApi = {
  // 查询
  getTree: () => request<IntentTreeResponse>("/api/intent/tree"),
  getClassifier: () => request<ClassifierConfig>("/api/intent/classifier"),
  getDefaultProvider: () => request<DefaultProvider>("/api/intent/default-provider"),

  // 分类
  classify: (question: string, autoSwitch = false) =>
    request<ClassifyResult>("/api/intent/classify", {
      method: "POST",
      body: JSON.stringify({ question, auto_switch: autoSwitch }),
    }),

  // CRUD
  createNode: (node: IntentNodeRecord) =>
    request<{ success: boolean }>("/api/intent/nodes", {
      method: "POST",
      body: JSON.stringify(node),
    }),

  updateNode: (intentCode: string, node: Partial<IntentNodeRecord>) =>
    request<{ success: boolean }>(`/api/intent/nodes/${intentCode}`, {
      method: "PATCH",
      body: JSON.stringify(node),
    }),

  deleteNode: (intentCode: string) =>
    request<void>(`/api/intent/nodes/${intentCode}`, { method: "DELETE" }),
};
