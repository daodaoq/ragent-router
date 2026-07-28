// Package timeout 提供基于 Go context 的级联超时控制。
//
// # 级联超时
//
// 在多层调用链中（如：网关 → 重试循环 → 单次 HTTP 请求），
// 每一层都可能有自己的超时设置。级联超时的核心规则：
//
//	子 deadline = min(父 deadline, 自己的 timeout)
//
// 如果父 context 只剩 5 秒，子层设置 10 秒超时——实际生效的是 5 秒。
// 这防止了经典的"超时逃逸"问题：
//
//	重试循环每次 10s 超时 × 5 次重试 → 实际上跑了 50s，
//	因为父 context 没有设置 deadline 来约束。
//
// # 使用示例
//
//	// 总预算 30s
//	rootCtx := timeout.Cascading(ctx.Background(), 30*time.Second)
//
//	for i := 0; i < 3; i++ {
//	    // 每次尝试 10s，但受父 deadline 约束
//	    attemptCtx := timeout.Cascading(rootCtx, 10*time.Second)
//	    err := doRequest(attemptCtx)
//	    if err == nil { break }
//	}
//	// 三次重试的总耗时 ≤ 30s，不会逃逸。
package timeout

import (
	"context"
	"time"
)

// Cascading 创建一个受父 context deadline 约束的 context。
//
// 行为：
//   - 如果父 context 没有 deadline → 创建新的 deadline = parent + timeout
//   - 如果父 deadline 比 timeout 更宽松 → 创建新的 deadline = parent + timeout
//   - 如果父 deadline 比 timeout 更紧 → 直接返回 parent 的 cancel（子 deadline 被父覆盖）
//
// 第三个分支是关键：父 deadline 更紧时，子层请求的"10 秒超时"
// 实际上被父的 5 秒 deadline 覆盖——这正是级联约束的语义。
func Cascading(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	// 检查父 context 是否已有更紧的 deadline。
	if deadline, ok := parent.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout {
			// 父 deadline 比我的 timeout 更紧——直接用父的。
			// 用 WithCancel 而非 WithTimeout 避免覆盖父的 deadline。
			return context.WithCancel(parent)
		}
	}
	return context.WithTimeout(parent, timeout)
}

// WithBudget 创建带有总时间预算的 context。
// 语义同 context.WithTimeout，但命名更清晰地表达
// "这是一个预算，子层会根据预算做取舍"。
func WithBudget(parent context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, budget)
}

// Remaining 返回 context 的剩余时间。
// 用于在调用链中间检查"还剩多少时间"来决定是否继续。
//
// 返回值：
//   - 有 deadline 且未过期 → 剩余时间（正数）
//   - 无 deadline → 0（无法判断剩余时间）
//   - 已过期 → 0
func Remaining(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining < 0 {
		return 0
	}
	return remaining
}
