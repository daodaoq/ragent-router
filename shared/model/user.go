package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// 角色常量（与 constant 包同步，避免循环引用）
const (
	RoleCommonUser = 1
	RoleAdminUser  = 10
	RoleRootUser   = 100
)

// 用户状态
const (
	UserStatusEnabled  = 1
	UserStatusDisabled = 2
)

// User 用户模型。
type User struct {
	Id            int            `json:"id"`
	Username      string         `json:"username" gorm:"uniqueIndex;size:20"`
	Password      string         `json:"-" gorm:"not null"`
	DisplayName   string         `json:"display_name" gorm:"size:20"`
	Role          int            `json:"role" gorm:"default:1"`
	Status        int            `json:"status" gorm:"default:1"`
	Email         string         `json:"email" gorm:"index;size:50"`
	GitHubId      string         `json:"-" gorm:"column:github_id;index"`
	WeChatId      string         `json:"-" gorm:"column:wechat_id;index"`
	TelegramId    string         `json:"-" gorm:"column:telegram_id;index"`
	AccessToken   *string        `json:"-" gorm:"type:char(32);column:access_token;uniqueIndex"`
	Quota         int            `json:"quota" gorm:"default:0"`
	UsedQuota     int            `json:"used_quota" gorm:"default:0"`
	RequestCount  int            `json:"request_count" gorm:"default:0"`
	Group         string         `json:"group" gorm:"type:varchar(64);default:'default'"`
	Setting       string         `json:"setting" gorm:"type:text"`
	Remark        string         `json:"remark" gorm:"type:varchar(255)"`
	CreatedAt     int64          `json:"created_at" gorm:"autoCreateTime"`
	LastLoginAt   int64          `json:"last_login_at" gorm:"default:0"`
	AuthVersion   int64          `json:"-" gorm:"not null;default:1"`
	DeletedAt     gorm.DeletedAt `gorm:"index"`
}

// TableName 指定表名。
func (User) TableName() string {
	return "users"
}

// HashPassword 对密码进行 bcrypt 哈希。
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPassword 验证密码。
func (u *User) CheckPassword(password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password))
	return err == nil
}

// GetUserById 按 ID 获取用户。
func GetUserById(id int) (*User, error) {
	var user User
	err := DB.Where("id = ?", id).First(&user).Error
	return &user, err
}

// GetUserByUsername 按用户名获取用户。
func GetUserByUsername(username string) (*User, error) {
	var user User
	err := DB.Where("username = ?", username).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &user, err
}

// ValidateAccessToken 验证管理 access token。
func ValidateAccessToken(token string) (*User, error) {
	if token == "" {
		return nil, nil
	}
	var user User
	err := DB.Where("access_token = ?", token).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &user, err
}

// UpdateUser 更新用户信息。
func UpdateUser(user *User) error {
	return DB.Save(user).Error
}

// DeleteUser 软删除用户。
func DeleteUser(id int) error {
	return DB.Delete(&User{}, id).Error
}

// GetAllUsers 获取用户列表（分页）。
func GetAllUsers(startIdx, num int, sortBy, sortOrder string) ([]*User, int64, error) {
	var users []*User
	var total int64

	DB.Model(&User{}).Count(&total)

	query := DB.Model(&User{})
	if sortBy != "" && sortOrder != "" {
		query = query.Order(fmt.Sprintf("%s %s", sortBy, sortOrder))
	} else {
		query = query.Order("id desc")
	}

	err := query.Offset(startIdx).Limit(num).Find(&users).Error
	return users, total, err
}

// IncreaseUserQuota 增加用户配额。
func IncreaseUserQuota(uid, quota int) error {
	return DB.Model(&User{}).Where("id = ?", uid).
		UpdateColumn("quota", gorm.Expr("quota + ?", quota)).Error
}

// DecreaseUserQuota 减少用户配额。
func DecreaseUserQuota(uid, quota int) error {
	return DB.Model(&User{}).Where("id = ?", uid).
		UpdateColumn("quota", gorm.Expr("quota - ?", quota)).Error
}

// IncreaseUsedQuota 增加已用配额。
func IncreaseUsedQuota(uid, quota int) error {
	return DB.Model(&User{}).Where("id = ?", uid).
		UpdateColumn("used_quota", gorm.Expr("used_quota + ?", quota)).Error
}

// IsAdmin 判断用户是否为管理员。
func IsAdmin(uid int) bool {
	var user User
	if err := DB.Select("role").Where("id = ?", uid).First(&user).Error; err != nil {
		return false
	}
	return user.Role >= RoleAdminUser
}

// LoginUser 用户登录验证。
func LoginUser(username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, fmt.Errorf("用户名和密码不能为空")
	}

	user, err := GetUserByUsername(username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, fmt.Errorf("用户不存在")
	}
	if user.Status != UserStatusEnabled {
		return nil, fmt.Errorf("用户已被禁用")
	}
	if !user.CheckPassword(password) {
		return nil, fmt.Errorf("密码错误")
	}

	// 更新最后登录时间
	DB.Model(user).UpdateColumn("last_login_at", time.Now().Unix())
	return user, nil
}
