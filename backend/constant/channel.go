// Package constant 定义全局常量。
package constant

// 用户角色常量
const (
	RoleCommonUser = 1
	RoleAdminUser  = 10
	RoleRootUser   = 100
)

// 用户状态常量
const (
	UserStatusEnabled  = 1
	UserStatusDisabled = 2 // 封禁
)

// Token 状态常量
const (
	TokenStatusEnabled   = 1
	TokenStatusDisabled  = 2
	TokenStatusExhausted = 3
)

// 渠道类型常量（59 种）
const (
	ChannelOpenAI          = 1
	ChannelAnthropic       = 14
	ChannelAzure           = 8
	ChannelGoogleGemini    = 24
	ChannelDeepSeek        = 25
	ChannelAli             = 17
	ChannelBaidu           = 18
	ChannelZhipu           = 16
	ChannelMoonshot        = 19
	ChannelMiniMax         = 21
	ChannelTencent         = 20
	ChannelXunfei          = 22
	ChannelVolcEngine      = 23
	ChannelMistral         = 12
	ChannelCohere          = 13
	ChannelPerplexity      = 26
	ChannelXAIGrok         = 37
	ChannelOllama          = 27
	ChannelOpenRouter      = 30
	ChannelSiliconFlow     = 31
	ChannelCloudflare      = 32
	ChannelAWSBedrock      = 33
	ChannelGoogleVertex    = 34
	ChannelDify            = 35
	ChannelJina            = 36
	ChannelReplicate       = 38
	ChannelCoze            = 39
	ChannelCustom          = 2
	ChannelUnknown         = 0
)

// ChannelBaseURLs 渠道类型对应的默认 Base URL。
var ChannelBaseURLs = map[int]string{
	ChannelOpenAI:       "https://api.openai.com",
	ChannelAnthropic:    "https://api.anthropic.com",
	ChannelAzure:        "",
	ChannelGoogleGemini: "https://generativelanguage.googleapis.com",
	ChannelDeepSeek:     "https://api.deepseek.com",
	ChannelAli:          "https://dashscope.aliyuncs.com/compatible-mode",
	ChannelBaidu:        "https://aip.baidubce.com",
	ChannelZhipu:        "https://open.bigmodel.cn/api/paas",
	ChannelMoonshot:     "https://api.moonshot.cn",
	ChannelMiniMax:      "https://api.minimax.chat",
	ChannelTencent:      "https://hunyuan.tencentcloudapi.com",
	ChannelXunfei:       "https://spark-api-open.xf-yun.com",
	ChannelVolcEngine:   "https://ark.cn-beijing.volces.com",
	ChannelMistral:      "https://api.mistral.ai",
	ChannelCohere:       "https://api.cohere.com",
	ChannelPerplexity:   "https://api.perplexity.ai",
	ChannelXAIGrok:      "https://api.x.ai",
	ChannelOllama:       "http://localhost:11434",
	ChannelOpenRouter:   "https://openrouter.ai/api",
	ChannelSiliconFlow:  "https://api.siliconflow.cn",
	ChannelCloudflare:   "https://api.cloudflare.com",
	ChannelAWSBedrock:   "",
	ChannelGoogleVertex: "",
	ChannelDify:         "",
	ChannelJina:         "https://api.jina.ai",
	ChannelReplicate:    "https://api.replicate.com",
	ChannelCoze:         "https://api.coze.com",
}

// ChannelNames 渠道类型对应的显示名称。
var ChannelNames = map[int]string{
	ChannelOpenAI:       "OpenAI",
	ChannelAnthropic:    "Anthropic",
	ChannelAzure:        "Azure OpenAI",
	ChannelGoogleGemini: "Google Gemini",
	ChannelDeepSeek:     "DeepSeek",
	ChannelAli:          "阿里通义",
	ChannelBaidu:        "百度文心",
	ChannelZhipu:        "智谱 GLM",
	ChannelMoonshot:     "月之暗面 Kimi",
	ChannelMiniMax:      "MiniMax",
	ChannelTencent:      "腾讯混元",
	ChannelXunfei:       "讯飞星火",
	ChannelVolcEngine:   "火山引擎豆包",
	ChannelMistral:      "Mistral",
	ChannelCohere:       "Cohere",
	ChannelPerplexity:   "Perplexity",
	ChannelXAIGrok:      "xAI Grok",
	ChannelOllama:       "Ollama",
	ChannelOpenRouter:   "OpenRouter",
	ChannelSiliconFlow:  "SiliconFlow",
	ChannelCloudflare:   "Cloudflare Workers AI",
	ChannelAWSBedrock:   "AWS Bedrock",
	ChannelGoogleVertex: "Google Vertex AI",
	ChannelDify:         "Dify",
	ChannelJina:         "Jina",
	ChannelReplicate:    "Replicate",
	ChannelCoze:         "Coze",
}
