package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Token 状态常量
const (
	TokenStatusEnabled   = 1
	TokenStatusDisabled  = 2
	TokenStatusExhausted = 3
)

// Token API Key 模型。
type Token struct {
	Id                 int            `json:"id"`
	UserId             int            `json:"user_id" gorm:"index"`
	Key                string         `json:"-" gorm:"type:varchar(128);uniqueIndex"`
	KeyHash            string         `json:"-" gorm:"type:varchar(64);index"`
	Status             int            `json:"status" gorm:"default:1"`
	Name               string         `json:"name" gorm:"index"`
	CreatedTime        int64          `json:"created_time" gorm:"bigint"`
	AccessedTime       int64          `json:"accessed_time" gorm:"bigint"`
	ExpiredTime        int64          `json:"expired_time" gorm:"bigint;default:-1"` // -1 = 永不过期
	RemainQuota        int            `json:"remain_quota" gorm:"default:0"`
	UnlimitedQuota     bool           `json:"unlimited_quota"`
	ModelLimitsEnabled bool           `json:"model_limits_enabled"`
	ModelLimits        string         `json:"model_limits" gorm:"type:text"`
	AllowIps           string         `json:"allow_ips" gorm:"default:''"`
	UsedQuota          int            `json:"used_quota" gorm:"default:0"`
	Group              string         `json:"group" gorm:"default:'default'"`
	DeletedAt          gorm.DeletedAt `gorm:"index"`
}

// TableName 指定表名。
func (Token) TableName() string {
	return "tokens"
}

// MaskTokenKey 掩码显示 API Key。
func MaskTokenKey(key string) string {
	if len(key) <= 8 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + "**********" + key[len(key)-4:]
}

// HashKey 计算 Key 的 SHA256 哈希（用于数据库索引）。
func HashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// GenerateTokenKey 生成新的 API Key。
func GenerateTokenKey() string {
	return "sk-" + uuid.New().String()
}

// CreateToken 创建新的 API Key。
func CreateToken(userId int, name string, quota int, expiredTime int64) (*Token, string, error) {
	rawKey := GenerateTokenKey()
	token := &Token{
		UserId:      userId,
		Key:         rawKey,
		KeyHash:     HashKey(rawKey),
		Status:      TokenStatusEnabled,
		Name:        name,
		CreatedTime: time.Now().Unix(),
		ExpiredTime: expiredTime,
		RemainQuota: quota,
	}
	err := DB.Create(token).Error
	return token, rawKey, err
}

// ValidateToken 验证 API Key 有效性。
// key 格式: sk-xxx 或 sk-xxx-channelId
func ValidateToken(key string) (*Token, error) {
	// 提取 key（去除 sk- 前缀和 channelId 后缀）
	key = strings.TrimPrefix(key, "sk-")
	parts := strings.Split(key, "-")
	key = parts[0]

	// 使用哈希索引查找
	keyHash := HashKey(key)
	var token Token
	err := DB.Where("key_hash = ?", keyHash).First(&token).Error
	if err != nil {
		return nil, fmt.Errorf("无效的 API Key")
	}

	// 检查状态
	if token.Status == TokenStatusDisabled {
		return nil, fmt.Errorf("API Key 已被禁用")
	}
	if token.Status == TokenStatusExhausted {
		return nil, fmt.Errorf("API Key 配额已耗尽")
	}

	// 检查过期
	if token.ExpiredTime != -1 && token.ExpiredTime < time.Now().Unix() {
		return nil, fmt.Errorf("API Key 已过期")
	}

	// 检查配额
	if !token.UnlimitedQuota && token.RemainQuota <= 0 {
		return nil, fmt.Errorf("API Key 配额不足")
	}

	// 更新访问时间
	DB.Model(&token).UpdateColumn("accessed_time", time.Now().Unix())

	return &token, nil
}

// GetTokenById 按 ID 获取 Token。
func GetTokenById(id int) (*Token, error) {
	var token Token
	err := DB.Where("id = ?", id).First(&token).Error
	return &token, err
}

// GetUserTokens 获取用户的所有 Token。
func GetUserTokens(userId int) ([]*Token, error) {
	var tokens []*Token
	err := DB.Where("user_id = ?", userId).Order("id desc").Find(&tokens).Error
	return tokens, err
}

// UpdateToken 更新 Token。
func UpdateToken(token *Token) error {
	return DB.Save(token).Error
}

// DeleteToken 删除 Token。
func DeleteToken(id, userId int) error {
	return DB.Where("id = ? AND user_id = ?", id, userId).Delete(&Token{}).Error
}

// DecreaseTokenQuota 减少 Token 配额。
func DecreaseTokenQuota(tokenId, quota int) error {
	return DB.Model(&Token{}).Where("id = ?", tokenId).
		UpdateColumn("remain_quota", gorm.Expr("remain_quota - ?", quota)).Error
}

// GetTokenByKey 通过原始 key 获取 Token（用于 Relay 认证）。
func GetTokenByKey(key string) (*Token, error) {
	keyHash := HashKey(key)
	var token Token
	err := DB.Where("key_hash = ?", keyHash).First(&token).Error
	if err != nil {
		return nil, err
	}
	return &token, nil
}
