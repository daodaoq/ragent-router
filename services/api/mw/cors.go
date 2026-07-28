package mw

import (
	"os"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// CORSMiddleware 返回 CORS 中间件配置。
func CORSMiddleware() gin.HandlerFunc {
	allowAll := os.Getenv("CORS_ALLOW_ALL") == "true"

	config := cors.Config{
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Api-Key", "Anthropic-Version", "X-Request-ID"},
		ExposeHeaders:    []string{"Content-Length", "X-Request-Id", "X-Ragent-Provider", "X-Ragent-Model", "X-Ragent-Reason"},
		AllowCredentials: true,
	}

	if allowAll {
		config.AllowAllOrigins = true
	} else {
		config.AllowOrigins = []string{
			"http://localhost:5173",
			"http://localhost:15722",
			"http://127.0.0.1:5173",
			"http://127.0.0.1:15722",
		}
	}

	return cors.New(config)
}
