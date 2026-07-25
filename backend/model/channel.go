package model

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// Channel 渠道模型——代表一个上游 LLM 提供商配置。
type Channel struct {
	Id                 int      `json:"id"`
	Type               int      `json:"type" gorm:"default:0"`
	Key                string   `json:"key" gorm:"not null"`
	TestModel          string   `json:"test_model"`
	Status             int      `json:"status" gorm:"default:1"` // 1=启用, 2=禁用, 3=自动禁用
	Name               string   `json:"name" gorm:"index"`
	Weight             int      `json:"weight" gorm:"default:1"`
	CreatedTime        int64    `json:"created_time" gorm:"bigint"`
	TestTime           int64    `json:"test_time" gorm:"bigint"`
	ResponseTime       int      `json:"response_time"` // 毫秒
	BaseURL            string   `json:"base_url" gorm:"column:base_url"`
	Balance            float64  `json:"balance"`
	BalanceUpdatedTime int64    `json:"balance_updated_time" gorm:"bigint"`
	Models             string   `json:"models"`              // 逗号分隔的模型列表
	Group              string   `json:"group" gorm:"type:varchar(64);default:'default'"`
	UsedQuota          int64    `json:"used_quota" gorm:"bigint;default:0"`
	ModelMapping       string   `json:"model_mapping" gorm:"type:text"`
	Priority           int64    `json:"priority" gorm:"bigint;default:0"`
	AutoBan            int      `json:"auto_ban" gorm:"default:1"` // 1=自动禁用, 0=手动管理
	Tag                string   `json:"tag" gorm:"index"`
	Remark             string   `json:"remark" gorm:"type:varchar(255)"`
}

// ChannelStatus 渠道状态常量
const (
	ChannelStatusEnabled       = 1
	ChannelStatusDisabled      = 2
	ChannelStatusAutoDisabled  = 3
)

// TableName 指定表名。
func (Channel) TableName() string {
	return "channels"
}

// Ability 渠道能力映射表——记录哪些渠道支持哪些模型。
type Ability struct {
	Id       int    `json:"id"`
	Channel  int    `json:"channel" gorm:"index"`
	Model    string `json:"model" gorm:"index"`
	Priority int64  `json:"priority" gorm:"bigint;default:0"`
	Weight   int    `json:"weight" gorm:"default:1"`
}

// TableName 指定表名。
func (Ability) TableName() string {
	return "abilities"
}

// GetChannelById 按 ID 获取渠道。
func GetChannelById(id int) (*Channel, error) {
	var channel Channel
	err := DB.Where("id = ?", id).First(&channel).Error
	return &channel, err
}

// GetAllChannels 获取所有渠道。
func GetAllChannels() ([]*Channel, error) {
	var channels []*Channel
	err := DB.Order("priority desc, id asc").Find(&channels).Error
	return channels, err
}

// GetEnabledChannels 获取所有启用的渠道。
func GetEnabledChannels() ([]*Channel, error) {
	var channels []*Channel
	err := DB.Where("status = ?", ChannelStatusEnabled).Order("priority desc").Find(&channels).Error
	return channels, err
}

// CreateChannel 创建渠道。
func CreateChannel(channel *Channel) error {
	channel.CreatedTime = time.Now().Unix()
	err := DB.Create(channel).Error
	if err != nil {
		return err
	}
	// 同步能力表
	return SyncChannelAbilities(channel)
}

// UpdateChannel 更新渠道。
func UpdateChannel(channel *Channel) error {
	err := DB.Save(channel).Error
	if err != nil {
		return err
	}
	return SyncChannelAbilities(channel)
}

// DeleteChannel 删除渠道。
func DeleteChannel(id int) error {
	DB.Where("channel = ?", id).Delete(&Ability{})
	return DB.Delete(&Channel{}, id).Error
}

// SyncChannelAbilities 同步渠道的能力映射。
func SyncChannelAbilities(channel *Channel) error {
	// 先删除旧的能力记录
	DB.Where("channel = ?", channel.Id).Delete(&Ability{})

	if channel.Models == "" {
		return nil
	}

	models := strings.Split(channel.Models, ",")
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		ability := &Ability{
			Channel:  channel.Id,
			Model:    model,
			Priority: channel.Priority,
			Weight:   channel.Weight,
		}
		DB.Create(ability)
	}
	return nil
}

// ── 渠道选择（带亲和性）──

var (
	channelCache     map[string][]*Channel // model → 可用渠道列表
	channelCacheMu   sync.RWMutex
	channelCacheTime time.Time
)

// RefreshChannelCache 刷新渠道缓存。
func RefreshChannelCache() error {
	channels, err := GetEnabledChannels()
	if err != nil {
		return err
	}

	cache := make(map[string][]*Channel)
	for _, ch := range channels {
		if ch.Models == "" {
			continue
		}
		for _, model := range strings.Split(ch.Models, ",") {
			model = strings.TrimSpace(model)
			if model != "" {
				cache[model] = append(cache[model], ch)
			}
		}
	}

	channelCacheMu.Lock()
	channelCache = cache
	channelCacheTime = time.Now()
	channelCacheMu.Unlock()
	return nil
}

// GetChannelForModel 为指定模型选择一个可用渠道（加权随机）。
func GetChannelForModel(modelName string) (*Channel, error) {
	channelCacheMu.RLock()
	channels, ok := channelCache[modelName]
	channelCacheMu.RUnlock()

	if !ok || len(channels) == 0 {
		// 缓存未命中，尝试刷新
		if err := RefreshChannelCache(); err != nil {
			return nil, err
		}
		channelCacheMu.RLock()
		channels, ok = channelCache[modelName]
		channelCacheMu.RUnlock()
		if !ok || len(channels) == 0 {
			return nil, fmt.Errorf("没有可用的渠道支持模型: %s", modelName)
		}
	}

	// 加权随机选择
	totalWeight := 0
	for _, ch := range channels {
		w := ch.Weight
		if w <= 0 {
			w = 1
		}
		totalWeight += w
	}

	r := rand.Intn(totalWeight)
	for _, ch := range channels {
		w := ch.Weight
		if w <= 0 {
			w = 1
		}
		r -= w
		if r < 0 {
			return ch, nil
		}
	}

	return channels[0], nil
}

// DisableChannel 禁用渠道（自动禁用）。
func DisableChannel(id int) error {
	return DB.Model(&Channel{}).Where("id = ?", id).
		UpdateColumn("status", ChannelStatusAutoDisabled).Error
}

// EnableChannel 启用渠道。
func EnableChannel(id int) error {
	return DB.Model(&Channel{}).Where("id = ?", id).
		UpdateColumn("status", ChannelStatusEnabled).Error
}

// GetChannelCount 获取渠道总数。
func GetChannelCount() int64 {
	var count int64
	DB.Model(&Channel{}).Count(&count)
	return count
}
