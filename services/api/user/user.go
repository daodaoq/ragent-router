// Package controller 提供 HTTP 请求处理器。
package user

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ragent/router/services/api/mw"
	"github.com/ragent/router/shared/model"
)

// Register 用户注册。
func Register(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required,min=3,max=20"`
		Password string `json:"password" binding:"required,min=6,max=20"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "参数错误: " + err.Error()})
		return
	}

	// 检查用户名是否已存在
	existing, _ := model.GetUserByUsername(req.Username)
	if existing != nil {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "用户名已存在"})
		return
	}

	hashedPwd, err := model.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "密码加密失败"})
		return
	}

	user := &model.User{
		Username: req.Username,
		Password: hashedPwd,
		Role:     model.RoleCommonUser,
		Status:   model.UserStatusEnabled,
		Group:    "default",
	}

	if err := model.DB.Create(user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "创建用户失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "注册成功",
		"user": gin.H{
			"id":       user.Id,
			"username": user.Username,
		},
	})
}

// Login 用户登录。
func Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "参数错误"})
		return
	}

	user, err := model.LoginUser(req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": err.Error()})
		return
	}

	token, err := mw.GenerateJWT(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "生成 Token 失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "登录成功",
		"data": gin.H{
			"token":    token,
			"username": user.Username,
			"role":     user.Role,
		},
	})
}

// GetSelf 获取当前用户信息。
func GetSelf(c *gin.Context) {
	userId := c.GetInt("id")
	user, err := model.GetUserById(userId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "获取用户信息失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":            user.Id,
			"username":      user.Username,
			"display_name":  user.DisplayName,
			"email":         user.Email,
			"role":          user.Role,
			"status":        user.Status,
			"group":         user.Group,
			"quota":         user.Quota,
			"used_quota":    user.UsedQuota,
			"request_count": user.RequestCount,
			"created_at":    user.CreatedAt,
			"last_login_at": user.LastLoginAt,
		},
	})
}

// GetUserList 获取用户列表（管理员）。
func GetUserList(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 10
	}
	startIdx := (page - 1) * size

	sortBy := c.Query("sort_by")
	sortOrder := c.Query("sort_order")

	users, total, err := model.GetAllUsers(startIdx, size, sortBy, sortOrder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "查询失败"})
		return
	}

	// 清除敏感信息
	for _, u := range users {
		u.Password = ""
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": users,
			"total": total,
			"page":  page,
			"size":  size,
		},
	})
}

// UpdateUser 更新用户信息（管理员）。
func UpdateUser(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少用户 ID"})
		return
	}

	user, err := model.GetUserById(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "用户不存在"})
		return
	}

	var req struct {
		DisplayName *string `json:"display_name"`
		Email       *string `json:"email"`
		Role        *int    `json:"role"`
		Status      *int    `json:"status"`
		Group       *string `json:"group"`
		Quota       *int    `json:"quota"`
		Remark      *string `json:"remark"`
		Password    *string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "参数错误"})
		return
	}

	if req.DisplayName != nil {
		user.DisplayName = *req.DisplayName
	}
	if req.Email != nil {
		user.Email = *req.Email
	}
	if req.Role != nil {
		// 只有 root 能修改角色
		currentRole := c.GetInt("role")
		if currentRole < model.RoleRootUser {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "只有 Root 能修改用户角色"})
			return
		}
		user.Role = *req.Role
	}
	if req.Status != nil {
		user.Status = *req.Status
	}
	if req.Group != nil {
		user.Group = *req.Group
	}
	if req.Quota != nil {
		user.Quota = *req.Quota
	}
	if req.Remark != nil {
		user.Remark = *req.Remark
	}
	if req.Password != nil && len(*req.Password) >= 6 {
		hashedPwd, err := model.HashPassword(*req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "密码加密失败"})
			return
		}
		user.Password = hashedPwd
	}

	if err := model.UpdateUser(user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "更新失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "更新成功"})
}

// DeleteUser 删除用户（管理员）。
func DeleteUser(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少用户 ID"})
		return
	}

	// 不能删除自己
	currentId := c.GetInt("id")
	if id == currentId {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "不能删除自己"})
		return
	}

	if err := model.DeleteUser(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "删除失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "删除成功"})
}

// SearchUsers 搜索用户。
func SearchUsers(c *gin.Context) {
	keyword := c.Query("keyword")
	if keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少搜索关键词"})
		return
	}

	var users []model.User
	model.DB.Where("username LIKE ? OR email LIKE ? OR display_name LIKE ?",
		"%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%").
		Limit(20).Find(&users)

	for i := range users {
		users[i].Password = ""
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": users})
}
