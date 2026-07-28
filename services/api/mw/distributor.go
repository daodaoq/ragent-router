package mw

import (
	"log"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ragent/router/shared/model"
)

// Distributor 渠道分发中间件。
// 根据请求中的 model 名称选择合适的上游渠道。
func Distributor() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 提取 model 名称
		modelName := extractModelFromRequest(c)
		if modelName == "" {
			// 没有 model 名称，继续（可能不是 relay 请求）
			c.Next()
			return
		}

		// 检查 token 的模型限制
		tokenModelLimit, exists := c.Get("token_model_limit")
		if exists {
			limits, ok := tokenModelLimit.(map[string]bool)
			if ok && !limits[modelName] {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error": gin.H{
						"message": "该 Token 无权访问模型: " + modelName,
						"type":    "invalid_request_error",
					},
				})
				return
			}
		}

		// 选择渠道
		channel, err := model.GetChannelForModel(modelName)
		if err != nil {
			 log.Printf("渠道选择失败: model=%s, err=%v", modelName, err)
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": gin.H{
					"message": "没有可用的渠道支持该模型",
					"type":    "server_error",
				},
			})
			return
		}

		// 设置渠道信息到上下文
		c.Set("channel_id", channel.Id)
		c.Set("channel_name", channel.Name)
		c.Set("channel_type", channel.Type)
		c.Set("channel_base_url", channel.BaseURL)
		c.Set("channel_key", channel.Key)
		c.Set("channel_model_mapping", channel.ModelMapping)
		c.Set("selected_model", modelName)

		c.Next()
	}
}

// extractModelFromRequest 从请求体中提取 model 名称。
func extractModelFromRequest(c *gin.Context) string {
	// 从 URL 路径中提取（如 /v1/chat/completions 中的 model）
	// 先尝试从 Content-Type 判断
	contentType := c.GetHeader("Content-Type")

	if strings.Contains(contentType, "multipart/form-data") {
		return c.PostForm("model")
	}

	// JSON 请求体
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	defer c.Request.Body.Close()

	// 恢复 body（后续 handler 还需要读取）
	c.Request.Body = io.NopCloser(strings.NewReader(string(body)))

	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	return req.Model
}
