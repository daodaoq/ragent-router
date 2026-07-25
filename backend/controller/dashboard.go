package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ragent/router/model"
)

// GetDashboardOverview 获取仪表盘概览。
func GetDashboardOverview(c *gin.Context) {
	overview, err := model.DashboardOverviewQuery()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": overview})
}

// GetModelDistribution 获取模型分布。
func GetModelDistribution(c *gin.Context) {
	items, err := model.ModelDistributionQuery()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

// GetCostTrend 获取成本趋势。
func GetCostTrend(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if days < 1 {
		days = 7
	}
	if days > 90 {
		days = 90
	}

	points, err := model.CostTrendQuery(days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": points})
}

// GetMonitorOverview 获取监控概览。
func GetMonitorOverview(c *gin.Context) {
	data, err := model.MonitorOverviewQuery()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// GetByModel 获取按模型统计。
func GetByModel(c *gin.Context) {
	items, err := model.ByModelQuery()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

// GetRecentLogs 获取最近请求日志。
func GetRecentLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit < 1 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	var logs []model.RequestLog
	model.DB.Order("id desc").Limit(limit).Find(&logs)

	c.JSON(http.StatusOK, gin.H{"success": true, "data": logs})
}
