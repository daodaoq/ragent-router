package model

import (
	"strings"
	"sync"
	"time"
)

// Channel 渠道模型。
type Channel struct {
	Id           int    `json:"id"`
	Type         int    `json:"type"`
	Key          string `json:"key"`
	TestModel    string `json:"test_model"`
	Status       int    `json:"status"` // 1=启用, 2=禁用, 3=自动禁用
	Name         string `json:"name"`
	Weight       int    `json:"weight"`
	CreatedTime  int64  `json:"created_time"`
	TestTime     int64  `json:"test_time"`
	ResponseTime int    `json:"response_time"`
	BaseURL      string `json:"base_url"`
	Models       string `json:"models"`
	Group        string `json:"group"`
	Priority     int64  `json:"priority"`
	AutoBan      int    `json:"auto_ban"`
	Tag          string `json:"tag"`
	Remark       string `json:"remark"`
}

const (
	ChannelStatusEnabled      = 1
	ChannelStatusDisabled     = 2
	ChannelStatusAutoDisabled = 3
)

// ChannelCache 渠道缓存（带读写锁）。
type ChannelCache struct {
	mu       sync.RWMutex
	channels map[string][]*Channel // model → channels
	all      []*Channel
}

var GlobalChannelCache = &ChannelCache{
	channels: make(map[string][]*Channel),
}

// Refresh 刷新缓存。
func (c *ChannelCache) Refresh(channels []*Channel) {
	cache := make(map[string][]*Channel)
	for _, ch := range channels {
		if ch.Models == "" || ch.Status != ChannelStatusEnabled {
			continue
		}
		for _, m := range strings.Split(ch.Models, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				cache[m] = append(cache[m], ch)
			}
		}
	}
	c.mu.Lock()
	c.channels = cache
	c.all = channels
	c.mu.Unlock()
}

// GetByModel 获取支持指定模型的渠道列表。
func (c *ChannelCache) GetByModel(model string) []*Channel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.channels[model]
}

// GetAll 获取所有渠道。
func (c *ChannelCache) GetAll() []*Channel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]*Channel, len(c.all))
	copy(result, c.all)
	return result
}

// HealthStatus 供应商健康状态。
type HealthStatus struct {
	ChannelId    int       `json:"channel_id"`
	Healthy      bool      `json:"healthy"`
	LastCheck    time.Time `json:"last_check"`
	FailCount    int       `json:"fail_count"`
	LatencyMs    int       `json:"latency_ms"`
	ErrorMessage string    `json:"error_message"`
}
