package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ragent/router/model"
)

// GetChannelList 获取渠道列表。
func GetChannelList(c *gin.Context) {
	channels, err := model.GetAllChannels()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": channels})
}

// CreateChannel 创建渠道。
func CreateChannel(c *gin.Context) {
	var channel model.Channel
	if err := c.ShouldBindJSON(&channel); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "参数错误: " + err.Error()})
		return
	}

	if channel.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "渠道名称不能为空"})
		return
	}
	if channel.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "API Key 不能为空"})
		return
	}

	if err := model.CreateChannel(&channel); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "创建失败"})
		return
	}

	// 刷新渠道缓存
	model.RefreshChannelCache()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "创建成功",
		"data":    channel,
	})
}

// UpdateChannel 更新渠道。
func UpdateChannel(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少渠道 ID"})
		return
	}

	channel, err := model.GetChannelById(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "渠道不存在"})
		return
	}

	var req struct {
		Name         *string  `json:"name"`
		Type         *int     `json:"type"`
		Key          *string  `json:"key"`
		BaseURL      *string  `json:"base_url"`
		Models       *string  `json:"models"`
		Weight       *int     `json:"weight"`
		Priority     *int64   `json:"priority"`
		Status       *int     `json:"status"`
		Group        *string  `json:"group"`
		ModelMapping *string  `json:"model_mapping"`
		TestModel    *string  `json:"test_model"`
		AutoBan      *int     `json:"auto_ban"`
		Tag          *string  `json:"tag"`
		Remark       *string  `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "参数错误"})
		return
	}

	if req.Name != nil {
		channel.Name = *req.Name
	}
	if req.Type != nil {
		channel.Type = *req.Type
	}
	if req.Key != nil {
		channel.Key = *req.Key
	}
	if req.BaseURL != nil {
		channel.BaseURL = *req.BaseURL
	}
	if req.Models != nil {
		channel.Models = *req.Models
	}
	if req.Weight != nil {
		channel.Weight = *req.Weight
	}
	if req.Priority != nil {
		channel.Priority = *req.Priority
	}
	if req.Status != nil {
		channel.Status = *req.Status
	}
	if req.Group != nil {
		channel.Group = *req.Group
	}
	if req.ModelMapping != nil {
		channel.ModelMapping = *req.ModelMapping
	}
	if req.TestModel != nil {
		channel.TestModel = *req.TestModel
	}
	if req.AutoBan != nil {
		channel.AutoBan = *req.AutoBan
	}
	if req.Tag != nil {
		channel.Tag = *req.Tag
	}
	if req.Remark != nil {
		channel.Remark = *req.Remark
	}

	if err := model.UpdateChannel(channel); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "更新失败"})
		return
	}

	model.RefreshChannelCache()

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "更新成功"})
}

// DeleteChannel 删除渠道。
func DeleteChannel(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少渠道 ID"})
		return
	}

	if err := model.DeleteChannel(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "删除失败"})
		return
	}

	model.RefreshChannelCache()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "删除成功"})
}

// TestChannel 测试渠道连通性。
func TestChannel(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少渠道 ID"})
		return
	}

	channel, err := model.GetChannelById(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "渠道不存在"})
		return
	}

	// TODO: 实际测试连通性（发送一个简单的请求到上游）
	_ = channel
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "连通性测试功能待实现",
	})
}

// GetChannelTypes 获取支持的渠道类型列表。
func GetChannelTypes(c *gin.Context) {
	types := make([]gin.H, 0)
	for id, name := range ChannelNames {
		types = append(types, gin.H{
			"id":       id,
			"name":     name,
			"base_url": ChannelBaseURLs[id],
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": types})
}

// ChannelNames 渠道名称映射。
var ChannelNames = map[int]string{
	1:  "OpenAI",
	2:  "自定义",
	8:  "Azure OpenAI",
	12: "Mistral",
	13: "Cohere",
	14: "Anthropic",
	16: "智谱 GLM",
	17: "阿里通义",
	18: "百度文心",
	19: "月之暗面 Kimi",
	20: "腾讯混元",
	21: "MiniMax",
	22: "讯飞星火",
	23: "火山引擎豆包",
	24: "Google Gemini",
	25: "DeepSeek",
	26: "Perplexity",
	27: "Ollama",
	30: "OpenRouter",
	31: "SiliconFlow",
	32: "Cloudflare Workers AI",
	33: "AWS Bedrock",
	34: "Google Vertex AI",
	35: "Dify",
	36: "Jina",
	37: "xAI Grok",
	38: "Replicate",
	39: "Coze",
}

// ChannelBaseURLs 渠道默认 Base URL。
var ChannelBaseURLs = map[int]string{
	1:  "https://api.openai.com",
	8:  "",
	12: "https://api.mistral.ai",
	13: "https://api.cohere.com",
	14: "https://api.anthropic.com",
	16: "https://open.bigmodel.cn/api/paas",
	17: "https://dashscope.aliyuncs.com/compatible-mode",
	18: "https://aip.baidubce.com",
	19: "https://api.moonshot.cn",
	20: "https://hunyuan.tencentcloudapi.com",
	21: "https://api.minimax.chat",
	22: "https://spark-api-open.xf-yun.com",
	23: "https://ark.cn-beijing.volces.com",
	24: "https://generativelanguage.googleapis.com",
	25: "https://api.deepseek.com",
	26: "https://api.perplexity.ai",
	27: "http://localhost:11434",
	30: "https://openrouter.ai/api",
	31: "https://api.siliconflow.cn",
	32: "https://api.cloudflare.com",
	33: "",
	34: "",
	35: "",
	36: "https://api.jina.ai",
	37: "https://api.x.ai",
	38: "https://api.replicate.com",
	39: "https://api.coze.com",
}
