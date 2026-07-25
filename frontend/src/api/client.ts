// 统一的 HTTP 请求封装，支持 JWT 认证。

const BASE_URL = import.meta.env.VITE_API_BASE || "http://localhost:15722";

/** 获取存储的 JWT Token。 */
function getToken(): string | null {
  return localStorage.getItem("ragent_token");
}

/** 存储 JWT Token。 */
export function setToken(token: string) {
  localStorage.setItem("ragent_token", token);
}

/** 清除 JWT Token。 */
export function clearToken() {
  localStorage.removeItem("ragent_token");
}

/** 检查是否已登录。 */
export function isLoggedIn(): boolean {
  return !!getToken();
}

/**
 * 向后端发送 JSON 请求并获取结构化响应。
 * 自动附加 JWT 认证头，处理 401 跳转登录。
 */
export async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...((options?.headers as Record<string, string>) || {}),
  };
  if (token) {
    headers["Authorization"] = `Bearer ${token}`;
  }

  const res = await fetch(`${BASE_URL}${url}`, {
    ...options,
    headers,
  });

  if (res.status === 401) {
    clearToken();
    // 如果不在登录页，跳转到登录
    if (!window.location.pathname.includes("/login")) {
      window.location.reload();
    }
    throw new Error("认证已过期，请重新登录");
  }

  if (!res.ok) {
    const text = await res.text();
    throw new Error(`API error ${res.status}: ${text}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

/** 基础请求（无 JSON 解析），用于非 JSON 响应。 */
export async function fetchRaw(url: string, options?: RequestInit): Promise<Response> {
  const token = getToken();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...((options?.headers as Record<string, string>) || {}),
  };
  if (token) {
    headers["Authorization"] = `Bearer ${token}`;
  }

  const res = await fetch(`${BASE_URL}${url}`, {
    ...options,
    headers,
  });

  if (res.status === 401) {
    clearToken();
    throw new Error("认证已过期");
  }

  if (!res.ok) {
    const text = await res.text();
    throw new Error(`API error ${res.status}: ${text}`);
  }
  return res;
}

// ── 认证 API ──

export interface LoginResponse {
  success: boolean;
  message: string;
  data?: {
    token: string;
    username: string;
    role: number;
  };
}

export async function login(username: string, password: string): Promise<LoginResponse> {
  const res = await fetch(`${BASE_URL}/api/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  return res.json();
}

export async function register(username: string, password: string): Promise<LoginResponse> {
  const res = await fetch(`${BASE_URL}/api/auth/register`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  return res.json();
}
