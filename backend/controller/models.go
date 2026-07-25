package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ragent/router/model"
)

// GetModels 返回支持的模型列表（OpenAI 兼容格式）。
func GetModels(c *gin.Context) {
	// 从所有启用的渠道中收集模型
	channels, err := model.GetEnabledChannels()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"message": "查询失败", "type": "server_error"},
		})
		return
	}

	modelSet := make(map[string]bool)
	for _, ch := range channels {
		if ch.Models != "" {
			for _, m := range splitModels(ch.Models) {
				if m != "" {
					modelSet[m] = true
				}
			}
		}
	}

	// 构建 OpenAI 兼容的 models 响应
	data := make([]gin.H, 0, len(modelSet))
	for m := range modelSet {
		data = append(data, gin.H{
			"id":       m,
			"object":   "model",
			"owned_by": "ragent-router",
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}

func splitModels(models string) []string {
	// 简单的逗号分割
	result := []string{}
	current := ""
	for _, c := range models {
		if c == ',' {
			if current != "" {
				result = append(result, current)
			}
			current = ""
		} else if c != ' ' {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}
