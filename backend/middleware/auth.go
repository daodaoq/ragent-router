// Package middleware 提供 Gin 中间件。
package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/ragent/router/common"
	"github.com/ragent/router/model"
)

// JWTSecret JWT 签名密钥（从环境变量加载）。
var JWTSecret []byte

// InitAuth 初始化认证模块。
func InitAuth() {
	secret := common.GetEnv("JWT_SECRET", "")
	if secret == "" {
		secret = common.GenerateRandomKey(32)
		common.SysLog("未配置 JWT_SECRET，使用随机密钥（重启后失效）")
	}
	JWTSecret = []byte(secret)
}

// JWTClaims JWT 载荷。
type JWTClaims struct {
	UserId   int    `json:"user_id"`
	Username string `json:"username"`
	Role     int    `json:"role"`
	jwt.RegisteredClaims
}

// GenerateJWT 生成 JWT Token。
func GenerateJWT(user *model.User) (string, error) {
	claims := JWTClaims{
		UserId:   user.Id,
		Username: user.Username,
		Role:     user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().AddDate(0, 0, 7)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(JWTSecret)
}

// ParseJWT 解析 JWT Token。
func ParseJWT(tokenString string) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		return JWTSecret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*JWTClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, jwt.ErrSignatureInvalid
}

// UserAuth Dashboard 用户认证中间件。
func UserAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "未提供认证信息",
			})
			return
		}

		claims, err := ParseJWT(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "认证信息无效或已过期",
			})
			return
		}

		// 检查用户状态
		user, err := model.GetUserById(claims.UserId)
		if err != nil || user.Status != model.UserStatusEnabled {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "用户已被禁用",
			})
			return
		}

		c.Set("id", user.Id)
		c.Set("username", user.Username)
		c.Set("role", user.Role)
		c.Set("group", user.Group)
		c.Next()
	}
}

// AdminAuth 管理员认证中间件。
func AdminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		UserAuth()(c)
		if c.IsAborted() {
			return
		}
		role := c.GetInt("role")
		if role < model.RoleAdminUser {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "需要管理员权限",
			})
			return
		}
	}
}

// RootAuth Root 认证中间件。
func RootAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		UserAuth()(c)
		if c.IsAborted() {
			return
		}
		role := c.GetInt("role")
		if role < model.RoleRootUser {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "需要 Root 权限",
			})
			return
		}
	}
}

// TokenAuth Relay API Key 认证中间件。
func TokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := extractRelayKey(c)
		if key == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"message": "未提供 API Key",
					"type":    "invalid_request_error",
				},
			})
			return
		}

		token, err := model.ValidateToken(key)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"message": err.Error(),
					"type":    "invalid_request_error",
				},
			})
			return
		}

		// 设置上下文
		c.Set("id", token.UserId)
		c.Set("token_id", token.Id)
		c.Set("token_key", key)
		c.Set("token_quota", token.RemainQuota)
		c.Set("token_unlimited_quota", token.UnlimitedQuota)
		c.Set("group", token.Group)

		c.Next()
	}
}

// extractToken 从 Authorization 头提取 JWT。
func extractToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// extractRelayKey 从各种来源提取 Relay API Key。
func extractRelayKey(c *gin.Context) string {
	// 标准 Bearer token
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") || strings.HasPrefix(auth, "bearer ") {
		key := strings.TrimSpace(auth[7:])
		if key != "" {
			return key
		}
	}

	// Anthropic x-api-key
	if anthropicKey := c.GetHeader("x-api-key"); anthropicKey != "" {
		return anthropicKey
	}

	// Google x-goog-api-key
	if googKey := c.GetHeader("x-goog-api-key"); googKey != "" {
		return googKey
	}

	// Gemini query parameter
	if key := c.Query("key"); key != "" {
		return key
	}

	return ""
}
