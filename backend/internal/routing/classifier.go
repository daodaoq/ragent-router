// Package routing — LLM 意图分类器
//
// 当 Embedding 语义匹配置信度不足时（余弦相似度 < 阈值），
// 启用 LLM 分类器做最终裁决。
//
// # 为什么需要 LLM 分类器
//
// Embedding 的局限：
//   - 短文本歧义："帮我看看这个" → 意图不明（debug? review? qa?）
//   - 边界样本：prompt 与多个 intent 的相似度都接近阈值
//   - OOD 问题：训练 embedding 模型时未见过的领域术语
//
// LLM 分类器通过上下文推理解决歧义：
//   - 输入：分类 prompt（包含所有意图描述）+ 用户原始问题
//   - 输出：结构化 JSON {"intent": "xxx", "confidence": 0.95}
//   - 成本：单次分类 ~200 input + ~30 output tokens ≈ $0.0002（DeepSeek）
//
// # 分类 Prompt 设计
//
//   - 精炼（~200 tokens）：减少分类延迟和费用
//   - 结构化输出：强制 JSON 返回，便于解析
//   - 多语言：中英双语意图描述，适配中英文用户
//   - 容错：parseClassifyResponse 支持 JSON 提取 + 默认值回退
package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ────────────────────────────────────────────────────────────
// 分类器接口
// ────────────────────────────────────────────────────────────

// IntentClassifier 使用 LLM 对用户提示词进行意图分类。
//
// 实现者需要：
//   1. 将意图列表转换为分类 prompt
//   2. 调用 LLM API
//   3. 解析结构化响应
//
// 返回值是 Intent.Name，供 HybridRouter 查找目标供应商。
type IntentClassifier interface {
	// Classify 对用户提示词进行意图分类。
	//
	// 参数：
	//   - ctx：超时控制（推荐 10s）
	//   - prompt：用户原始提示词
	//   - intents：候选意图列表（用于构建分类 prompt）
	//
	// 返回：
	//   - intentName：分类结果的意图名称（对应 Intent.Name）
	//   - error：网络错误、超时、解析失败等
	Classify(ctx context.Context, prompt string, intents []Intent) (string, error)
}

// ────────────────────────────────────────────────────────────
// LLM 意图分类器实现
// ────────────────────────────────────────────────────────────

// LLMIntentClassifier 通过调用 LLM Chat API 进行意图分类。
//
// 分类流程：
//   1. buildClassifyPrompt：将意图列表格式化为分类 prompt
//   2. 发送 POST 请求到 Provider 的 /v1/chat/completions
//   3. parseClassifyResponse：提取 JSON 并解析 intent 字段
//
// 使用的模型：推荐 deepseek-chat（快、便宜、分类准确率 > 95%）。
type LLMIntentClassifier struct {
	endpoint  string       // Provider 的 chat completions 端点
	apiKey    string       // API 密钥
	model     string       // 用于分类的模型（推荐 deepseek-chat）
	client    *http.Client // HTTP 客户端
}

// ClassifierConfig 是 LLM 分类器的配置。
type ClassifierConfig struct {
	Endpoint string        // Chat API 端点，如 https://api.deepseek.com/v1/chat/completions
	APIKey   string        // API 密钥
	Model    string        // 模型名，如 deepseek-chat
	Timeout  time.Duration // 超时，默认 10 秒
}

// NewLLMIntentClassifier 创建一个 LLM 意图分类器。
//
// 推荐配置：
//   - Endpoint: https://api.deepseek.com/v1/chat/completions
//   - Model: deepseek-chat（速度快、成本低、分类效果好）
//   - Timeout: 10s（分类是简单任务，10 秒足够）
func NewLLMIntentClassifier(cfg ClassifierConfig) *LLMIntentClassifier {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &LLMIntentClassifier{
		endpoint: cfg.Endpoint,
		apiKey:   cfg.APIKey,
		model:    cfg.Model,
		client: &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     60 * time.Second,
			},
		},
	}
}

// ────────────────────────────────────────────────────────────
// 分类器核心逻辑
// ────────────────────────────────────────────────────────────

// Classify 执行 LLM 意图分类。
func (c *LLMIntentClassifier) Classify(ctx context.Context, prompt string, intents []Intent) (string, error) {
	systemPrompt := buildClassifyPrompt(intents)

	reqBody := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": fmt.Sprintf("请分类以下用户问题：\n\n%s", prompt)},
		},
		"temperature": 0.0, // 分类任务不需要创造性，temperature=0 保证确定性输出
		"max_tokens":  100,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal classify request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create classify request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("classify API call: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10)) // 限制 64KB
	if err != nil {
		return "", fmt.Errorf("read classify response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("classify API HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	return parseClassifyResponse(respBytes, intents)
}

// ────────────────────────────────────────────────────────────
// 分类 Prompt 构建
// ────────────────────────────────────────────────────────────

// buildClassifyPrompt 将意图列表构建为 LLM 分类 system prompt。
//
// Prompt 工程要点：
//   - 角色设定：明确告诉 LLM 它是分类器，设定行为约束
//   - 意图枚举：列出所有候选意图及其描述
//   - 输出格式：严格 JSON，方便程序解析
//   - 边界处理：unknown 意图 + 必须包含 confidence
//
// Token 预估：~200 input tokens（不含 prompt），~30 output tokens。
func buildClassifyPrompt(intents []Intent) string {
	var sb strings.Builder

	sb.WriteString("你是一个精确的意图分类器。")
	sb.WriteString("分析用户的问题，判断它最可能属于以下哪个意图类别。")
	sb.WriteString("如果问题模糊或无法明确分类，使用 unknown。\n\n")
	sb.WriteString("候选意图类别：\n")

	for i, intent := range intents {
		sb.WriteString(fmt.Sprintf("%d. %s：%s\n", i+1, intent.Name, intent.Description))
	}
	sb.WriteString(fmt.Sprintf("%d. unknown：无法明确分类的问题\n\n", len(intents)+1))

	sb.WriteString("只返回 JSON 对象，不要任何其他内容。格式：\n")
	sb.WriteString(`{"intent": "类别名", "confidence": 0.95}`)
	sb.WriteString("\n\n")
	sb.WriteString("confidence 是 0-1 之间的浮点数，表示你对分类结果的把握程度。")

	return sb.String()
}

// ────────────────────────────────────────────────────────────
// 分类结果解析
// ────────────────────────────────────────────────────────────

// classifyResult 是 LLM 分类的结构化输出。
type classifyResult struct {
	Intent     string  `json:"intent"`
	Confidence float64 `json:"confidence"`
}

// parseClassifyResponse 从 LLM 响应中提取意图分类结果。
//
// 容错策略（按顺序尝试）：
//   1. 直接 JSON 反序列化
//   2. 从文本中提取 JSON 块（LLM 可能在 JSON 前后加了说明文字）
//   3. 子串匹配 intent name（最后的兜底）
func parseClassifyResponse(respBytes []byte, intents []Intent) (string, error) {
	// ── 尝试 1：直接 JSON 解析 ──
	var result classifyResult
	if err := json.Unmarshal(respBytes, &result); err == nil && result.Intent != "" {
		return result.Intent, nil
	}

	// ── 尝试 2：从文本中提取 JSON 块 ──
	// 典型场景：LLM 返回 "好的，分类如下：\n{"intent": ...}\n"
	respStr := string(respBytes)
	if start := strings.Index(respStr, "{"); start >= 0 {
		if end := strings.LastIndex(respStr, "}"); end > start {
			jsonStr := respStr[start : end+1]
			if err := json.Unmarshal([]byte(jsonStr), &result); err == nil && result.Intent != "" {
				return result.Intent, nil
			}
		}
	}

	// ── 尝试 3：子串匹配 intent name（兜底策略）──
	// 遍历所有已知意图，检查响应中是否包含意图名称。
	// 按优先级降序，避免 simple_qa 在 "explain" 等场景中误匹配。
	respLower := strings.ToLower(respStr)
	for _, intent := range intents {
		if strings.Contains(respLower, strings.ToLower(intent.Name)) {
			return intent.Name, nil
		}
	}

	// ── 全部失败 ──
	return "", fmt.Errorf("failed to parse classify response: %s", string(respBytes))
}
