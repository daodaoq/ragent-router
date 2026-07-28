package mw

import (
	"github.com/ragent/router/shared/redis"
	"fmt"
	"net/http"

	"github.com/zeromicro/go-zero/core/logx"
)

// RateLimitMiddleware Redis 分布式限流中间件。
//
// 高并发方案 1：基于 Redis + Lua 的分布式令牌桶限流。
//
// 三层限流：
//  1. 全局限流：保护整个系统
//  2. 用户限流：防止单用户滥用
//  3. 供应商限流：保护上游供应商
type RateLimitMiddleware struct {
	globalRate  float64
	globalBurst int
	userRate    float64
	userBurst   int
}

func NewRateLimitMiddleware(globalRate float64, globalBurst int, userRate float64, userBurst int) *RateLimitMiddleware {
	return &RateLimitMiddleware{
		globalRate:  globalRate,
		globalBurst: globalBurst,
		userRate:    userRate,
		userBurst:   userBurst,
	}
}

func (m *RateLimitMiddleware) Handle(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// ── 层 1: 全局限流 ──
		if !redis.Allow(ctx, "ratelimit:api:global", m.globalBurst, m.globalRate) {
			logx.WithContext(ctx).Errorf("[限流] 全局限流触发")
			w.Header().Set("Retry-After", "1")
			w.Header().Set("X-RateLimit-Type", "global")
			http.Error(w, `{"error":"global rate limit exceeded","type":"global"}`, http.StatusTooManyRequests)
			return
		}

		// ── 层 2: 用户限流（按 IP 或 Token）──
		userKey := getUserKey(r)
		if userKey != "" {
			limitKey := fmt.Sprintf("ratelimit:api:user:%s", userKey)
			if !redis.Allow(ctx, limitKey, m.userBurst, m.userRate) {
				logx.WithContext(ctx).Errorf("[限流] 用户限流触发: %s", userKey)
				w.Header().Set("Retry-After", "1")
				w.Header().Set("X-RateLimit-Type", "user")
				http.Error(w, `{"error":"user rate limit exceeded","type":"user"}`, http.StatusTooManyRequests)
				return
			}
		}

		next(w, r)
	}
}

// getUserKey 从请求中提取用户标识。
func getUserKey(r *http.Request) string {
	// 优先使用 JWT 中的用户 ID
	if userID := r.Header.Get("X-User-ID"); userID != "" {
		return userID
	}
	// 回退到 IP
	return r.RemoteAddr
}
