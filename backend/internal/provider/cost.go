package provider

// CostRate 定义单个模型的输入/输出 Token 费率（美元/百万 Token）。
type CostRate struct {
	Input  float64
	Output float64
}

// DefaultRates 返回供应商默认费率表。
// 键为供应商名（大小写不敏感匹配），值为输入/输出费率（美元/百万 Token）。
//
// 注意：费率会随供应商调价而变化，不同模型（如 claude-sonnet vs claude-opus）
// 费率也不同。当前按供应商粗略估算，精确计费需按模型名匹配。
func DefaultRates() map[string]CostRate {
	return map[string]CostRate{
		"deepseek": {0.27, 1.10},
		"claude":   {3.00, 15.00},
		"openai":   {2.50, 10.00},
		"minimax":  {0.30, 1.20},
		"bailian":  {0.40, 1.20},
	}
}
