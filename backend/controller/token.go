package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ragent/router/model"
)

// GetTokenList 获取当前用户的 API Key 列表。
func GetTokenList(c *gin.Context) {
	userId := c.GetInt("id")
	tokens, err := model.GetUserTokens(userId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "查询失败"})
		return
	}

	// 掩码显示 key
	type tokenResp struct {
		Id             int    `json:"id"`
		Name           string `json:"name"`
		Key            string `json:"key"` // 掩码后的
		Status         int    `json:"status"`
		CreatedTime    int64  `json:"created_time"`
		AccessedTime   int64  `json:"accessed_time"`
		ExpiredTime    int64  `json:"expired_time"`
		RemainQuota    int    `json:"remain_quota"`
		UnlimitedQuota bool   `json:"unlimited_quota"`
		UsedQuota      int    `json:"used_quota"`
		Group          string `json:"group"`
	}

	items := make([]tokenResp, 0, len(tokens))
	for _, t := range tokens {
		items = append(items, tokenResp{
			Id:             t.Id,
			Name:           t.Name,
			Key:            model.MaskTokenKey(t.Key),
			Status:         t.Status,
			CreatedTime:    t.CreatedTime,
			AccessedTime:   t.AccessedTime,
			ExpiredTime:    t.ExpiredTime,
			RemainQuota:    t.RemainQuota,
			UnlimitedQuota: t.UnlimitedQuota,
			UsedQuota:      t.UsedQuota,
			Group:          t.Group,
		})
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

// CreateToken 创建 API Key。
func CreateToken(c *gin.Context) {
	userId := c.GetInt("id")

	var req struct {
		Name           string `json:"name" binding:"required"`
		Quota          int    `json:"quota"`
		ExpiredTime    int64  `json:"expired_time"`    // -1 = 永不过期
		UnlimitedQuota bool   `json:"unlimited_quota"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "参数错误: " + err.Error()})
		return
	}

	if req.ExpiredTime == 0 {
		req.ExpiredTime = -1 // 默认永不过期
	}

	// 检查用户 Token 数量限制
	var count int64
	model.DB.Model(&model.Token{}).Where("user_id = ?", userId).Count(&count)
	if count >= 100 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Token 数量已达上限 (100)"})
		return
	}

	token, rawKey, err := model.CreateToken(userId, req.Name, req.Quota, req.ExpiredTime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "创建失败"})
		return
	}

	if req.UnlimitedQuota {
		token.UnlimitedQuota = true
		model.UpdateToken(token)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "创建成功，请妥善保管 API Key（仅显示一次）",
		"data": gin.H{
			"id":      token.Id,
			"name":    token.Name,
			"key":     rawKey, // 仅创建时返回完整 key
			"quota":   token.RemainQuota,
		},
	})
}

// UpdateToken 更新 API Key。
func UpdateToken(c *gin.Context) {
	userId := c.GetInt("id")
	tokenId, _ := strconv.Atoi(c.Param("id"))
	if tokenId == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少 Token ID"})
		return
	}

	token, err := model.GetTokenById(tokenId)
	if err != nil || token.UserId != userId {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Token 不存在"})
		return
	}

	var req struct {
		Name           *string `json:"name"`
		Quota          *int    `json:"quota"`
		Status         *int    `json:"status"`
		ExpiredTime    *int64  `json:"expired_time"`
		UnlimitedQuota *bool   `json:"unlimited_quota"`
		ModelLimits    *string `json:"model_limits"`
		AllowIps       *string `json:"allow_ips"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "参数错误"})
		return
	}

	if req.Name != nil {
		token.Name = *req.Name
	}
	if req.Quota != nil {
		token.RemainQuota = *req.Quota
	}
	if req.Status != nil {
		token.Status = *req.Status
	}
	if req.ExpiredTime != nil {
		token.ExpiredTime = *req.ExpiredTime
	}
	if req.UnlimitedQuota != nil {
		token.UnlimitedQuota = *req.UnlimitedQuota
	}
	if req.ModelLimits != nil {
		token.ModelLimits = *req.ModelLimits
	}
	if req.AllowIps != nil {
		token.AllowIps = *req.AllowIps
	}

	if err := model.UpdateToken(token); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "更新失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "更新成功"})
}

// DeleteToken 删除 API Key。
func DeleteToken(c *gin.Context) {
	userId := c.GetInt("id")
	tokenId, _ := strconv.Atoi(c.Param("id"))
	if tokenId == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少 Token ID"})
		return
	}

	if err := model.DeleteToken(tokenId, userId); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "删除失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "删除成功"})
}

// GetTokenStatus 获取 Token 状态（Relay 用）。
func GetTokenStatus(c *gin.Context) {
	tokenKey := c.GetString("token_key")
	if tokenKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少 Token"})
		return
	}

	token, err := model.GetTokenByKey(tokenKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Token 不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"name":            token.Name,
			"remain_quota":    token.RemainQuota,
			"unlimited_quota": token.UnlimitedQuota,
			"used_quota":      token.UsedQuota,
		},
	})
}
