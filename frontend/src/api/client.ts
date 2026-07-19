// 统一的 HTTP 请求封装，所有 API 模块的基础。

const BASE_URL = import.meta.env.VITE_API_BASE || "http://localhost:15722";

/**
 * 向后端发送 JSON 请求并获取结构化响应。
 * 自动处理错误状态码（非 2xx 抛出 Error）。
 */
export async function request<T>(url: string, options?: RequestInit): Promise<T> {
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

/** 基础请求（无 JSON 解析），用于非 JSON 响应。 */
export async function fetchRaw(url: string, options?: RequestInit): Promise<Response> {
  const res = await fetch(`${BASE_URL}${url}`, {
    headers: { "Content-Type": "application/json" },
    ...options,
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`API error ${res.status}: ${text}`);
  }
  return res;
}
