// Package router 提供 HTTP 路由注册。
package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ragent/router/controller"
	"github.com/ragent/router/middleware"
)

// ProxyHandler 是代理处理器的接口，避免直接依赖 internal 包。
type ProxyHandler interface {
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}

// SetRouter 注册所有路由。
func SetRouter(r *gin.Engine, proxyHandler ProxyHandler) {
	// ── 健康检查（无需认证）──
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "service": "ragent-router"})
	})
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"service": "ragent-router",
			"version": "0.3.0",
		})
	})
	r.GET("/readyz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// ── Relay 端点（API Key 认证）──
	v1 := r.Group("/v1")
	v1.Use(middleware.TokenAuth())
	{
		v1.POST("/messages", func(c *gin.Context) {
			proxyHandler.ServeHTTP(c.Writer, c.Request)
		})
		v1.POST("/chat/completions", func(c *gin.Context) {
			proxyHandler.ServeHTTP(c.Writer, c.Request)
		})
		v1.POST("/completions", func(c *gin.Context) {
			proxyHandler.ServeHTTP(c.Writer, c.Request)
		})
		v1.POST("/embeddings", func(c *gin.Context) {
			proxyHandler.ServeHTTP(c.Writer, c.Request)
		})
		v1.GET("/models", controller.GetModels)
	}

	// ── Dashboard API（JWT 认证）──
	api := r.Group("/api")
	{
		// 公开端点
		api.POST("/auth/register", controller.Register)
		api.POST("/auth/login", controller.Login)

		// 需要认证的端点
		auth := api.Group("")
		auth.Use(middleware.UserAuth())
		{
			// 用户
			auth.GET("/user/self", controller.GetSelf)

			// Token
			auth.GET("/tokens", controller.GetTokenList)
			auth.POST("/tokens", controller.CreateToken)
			auth.PUT("/tokens/:id", controller.UpdateToken)
			auth.DELETE("/tokens/:id", controller.DeleteToken)
			auth.GET("/tokens/:id/status", controller.GetTokenStatus)

			// Dashboard
			auth.GET("/dashboard/overview", controller.GetDashboardOverview)
			auth.GET("/dashboard/model-distribution", controller.GetModelDistribution)
			auth.GET("/dashboard/cost-trend", controller.GetCostTrend)
			auth.GET("/dashboard/recent-logs", controller.GetRecentLogs)

			// Monitor
			auth.GET("/monitor/overview", controller.GetMonitorOverview)
			auth.GET("/monitor/by-model", controller.GetByModel)
		}

		// 管理员端点
		admin := api.Group("")
		admin.Use(middleware.AdminAuth())
		{
			// 用户管理
			admin.GET("/users", controller.GetUserList)
			admin.PUT("/users/:id", controller.UpdateUser)
			admin.DELETE("/users/:id", controller.DeleteUser)
			admin.GET("/users/search", controller.SearchUsers)

			// 渠道管理
			admin.GET("/channels", controller.GetChannelList)
			admin.POST("/channels", controller.CreateChannel)
			admin.PUT("/channels/:id", controller.UpdateChannel)
			admin.DELETE("/channels/:id", controller.DeleteChannel)
			admin.POST("/channels/:id/test", controller.TestChannel)
			admin.GET("/channel-types", controller.GetChannelTypes)
		}

		// Root 端点
		root := api.Group("")
		root.Use(middleware.RootAuth())
		{
			// 系统配置
			root.GET("/system/info", func(c *gin.Context) {
				c.JSON(200, gin.H{"success": true, "data": gin.H{"version": "0.3.0"}})
			})
		}
	}
}
