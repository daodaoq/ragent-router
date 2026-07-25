package model

import "gorm.io/gorm"

// Option 系统配置项模型。
type Option struct {
	Key   string `json:"key" gorm:"primaryKey;size:100"`
	Value string `json:"value" gorm:"type:text"`
}

// TableName 指定表名。
func (Option) TableName() string {
	return "options"
}

// GetOption 获取配置值。
func GetOption(key string) (string, error) {
	var option Option
	err := DB.Where("key = ?", key).First(&option).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	return option.Value, err
}

// SetOption 设置配置值（upsert）。
func SetOption(key, value string) error {
	return DB.Where(Option{Key: key}).
		Assign(Option{Value: value}).
		FirstOrCreate(&Option{}).Error
}

// DeleteOption 删除配置。
func DeleteOption(key string) error {
	return DB.Where("key = ?", key).Delete(&Option{}).Error
}

// RedemptionCode 充值码模型。
type RedemptionCode struct {
	Id      int    `json:"id"`
	Code    string `json:"code" gorm:"uniqueIndex;size:32"`
	Quota   int    `json:"quota"`
	Used    bool   `json:"used" gorm:"default:false"`
	UsedBy  int    `json:"used_by" gorm:"default:0"`
	UsedAt  int64  `json:"used_at" gorm:"bigint"`
	Created int64  `json:"created" gorm:"bigint"`
}

// TableName 指定表名。
func (RedemptionCode) TableName() string {
	return "redemption_codes"
}
