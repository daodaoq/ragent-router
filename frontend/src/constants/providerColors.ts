// 供应商颜色映射（统一管理，大小写不敏感匹配）
const PROVIDER_COLORS: Record<string, string> = {
  deepseek: "green",
  claude: "purple",
  minimax: "red",
  bailian: "purple",
  openai: "green",
};

/** 根据供应商名称获取颜色，不区分大小写。 */
export function getProviderColor(name: string): string {
  return PROVIDER_COLORS[name.toLowerCase()] || "default";
}
