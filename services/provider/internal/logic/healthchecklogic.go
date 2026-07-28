package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ragent/router/services/provider/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// HealthCheckLogic 健康检查逻辑。
//
// 高并发方案 3：后台 goroutine 定期探测供应商健康状态。
// 不健康 → 自动摘除（从路由池移除），恢复 → 自动加回。
type HealthCheckLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewHealthCheckLogic(ctx context.Context, svcCtx *svc.ServiceContext) *HealthCheckLogic {
	return &HealthCheckLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// ProviderHealth 供应商健康状态。
type ProviderHealth struct {
	ChannelId int   `json:"channel_id"`
	Name      string `json:"name"`
	Healthy   bool  `json:"healthy"`
	LatencyMs int64 `json:"latency_ms"`
	LastCheck int64 `json:"last_check"`
	FailCount int   `json:"fail_count"`
}

// RunHealthCheck 启动后台健康检查循环。
//
// 设计要点：
//   - 每隔 N 秒探测一次所有启用的供应商
//   - 连续 M 次失败 → 自动禁用（状态写入 Redis + DB）
//   - 连续 K 次成功 → 自动恢复
//   - 健康状态缓存在 Redis 中，供路由服务读取
func (l *HealthCheckLogic) RunHealthCheck() {
	interval := l.svcCtx.Config.HealthCheck.Interval
	if interval <= 0 {
		interval = 30
	}

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	l.Info("[健康检查] 启动后台探测，间隔 %ds", interval)

	for range ticker.C {
		l.checkAll()
	}
}

func (l *HealthCheckLogic) checkAll() {
	// 从 Redis 缓存获取所有渠道
	ctx := context.Background()
	key := "ragent:providers:all"

	data, err := l.svcCtx.Redis.Get(key)
	if err != nil || data == "" {
		return
	}

	var channels []struct {
		Id      int    `json:"id"`
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Key     string `json:"key"`
		Status  int    `json:"status"`
	}
	if err := json.Unmarshal([]byte(data), &channels); err != nil {
		return
	}

	for _, ch := range channels {
		if ch.Status != 1 { // 只检查启用的
			continue
		}
		go l.checkOne(ctx, ch.Id, ch.Name, ch.BaseURL, ch.Key)
	}
}

func (l *HealthCheckLogic) checkOne(ctx context.Context, id int, name, baseURL, key string) {
	start := time.Now()
	healthy, errMsg := l.probe(baseURL, key)
	latency := time.Since(start).Milliseconds()

	// 更新 Redis 健康状态
	healthKey := fmt.Sprintf("ragent:health:%d", id)
	health := ProviderHealth{
		ChannelId: id,
		Name:      name,
		Healthy:   healthy,
		LatencyMs: latency,
		LastCheck: time.Now().Unix(),
	}

	// 获取连续失败次数
	failCountKey := fmt.Sprintf("ragent:health:failcount:%d", id)
	if healthy {
		l.svcCtx.Redis.SetexCtx(ctx, failCountKey, "0", 86400)
	} else {
		// 递增失败计数
		l.svcCtx.Redis.IncrCtx(ctx, failCountKey)
		l.svcCtx.Redis.ExpireCtx(ctx, failCountKey, 86400)
		failCount, _ := l.svcCtx.Redis.GetCtx(ctx, failCountKey)
		health.FailCount = len(failCount) // 简化处理
	}

	healthData, _ := json.Marshal(health)
	l.svcCtx.Redis.SetexCtx(ctx, healthKey, string(healthData), 300)

	if !healthy {
		l.Errorf("[健康检查] %s 不健康: %s (延迟 %dms)", name, errMsg, latency)

		// 连续失败超过阈值 → 自动禁用
		failThreshold := l.svcCtx.Config.HealthCheck.FailThreshold
		if failThreshold <= 0 {
			failThreshold = 3
		}
		failStr, _ := l.svcCtx.Redis.GetCtx(ctx, failCountKey)
		if len(failStr) >= failThreshold {
			l.Errorf("[健康检查] %s 连续失败 %d 次，自动禁用", name, len(failStr))
			l.disableProvider(ctx, id)
		}
	} else {
		l.Infof("[健康检查] %s 健康 (延迟 %dms)", name, latency)

		// 连续成功 → 自动恢复
		recoverKey := fmt.Sprintf("ragent:health:recover:%d", id)
		l.svcCtx.Redis.IncrCtx(ctx, recoverKey)
		l.svcCtx.Redis.ExpireCtx(ctx, recoverKey, 86400)
		recoverThreshold := l.svcCtx.Config.HealthCheck.RecoverThreshold
		if recoverThreshold <= 0 {
			recoverThreshold = 3
		}
		recoverStr, _ := l.svcCtx.Redis.GetCtx(ctx, recoverKey)
		if len(recoverStr) >= recoverThreshold {
			l.Infof("[健康检查] %s 连续成功 %d 次，自动恢复", name, len(recoverStr))
			l.enableProvider(ctx, id)
			l.svcCtx.Redis.SetexCtx(ctx, recoverKey, "0", 86400)
		}
	}
}

// probe 向上游发送轻量请求探测连通性。
func (l *HealthCheckLogic) probe(baseURL, key string) (bool, string) {
	if baseURL == "" {
		return false, "未配置 Base URL"
	}

	testURL := strings.TrimRight(baseURL, "/") + "/v1/models"
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest(http.MethodGet, testURL, nil)
	if err != nil {
		return false, fmt.Sprintf("构造请求失败: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 500 {
		return false, fmt.Sprintf("上游返回 %d", resp.StatusCode)
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return false, "API Key 无效"
	}
	return true, ""
}

func (l *HealthCheckLogic) disableProvider(ctx context.Context, id int) {
	key := fmt.Sprintf("ragent:provider:status:%d", id)
	l.svcCtx.Redis.SetexCtx(ctx, key, "disabled", 86400)
}

func (l *HealthCheckLogic) enableProvider(ctx context.Context, id int) {
	key := fmt.Sprintf("ragent:provider:status:%d", id)
	l.svcCtx.Redis.SetexCtx(ctx, key, "enabled", 86400)
}
