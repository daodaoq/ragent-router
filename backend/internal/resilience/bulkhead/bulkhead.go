// Package bulkhead 通过信号量模式限制并发调用数，
// 防止单个慢依赖耗尽所有 goroutine（舱壁隔离模式）。
//
// # 舱壁模式
//
// 名称来源于船舶设计：船体用舱壁分隔成多个水密隔舱——
// 一个隔舱进水不会导致整船沉没。在软件中，舱壁将系统资源
// 按依赖划分：一个慢 API 不会耗尽所有 goroutine/连接/线程，
// 其他依赖不受影响。
//
// # 实现方式
//
// 使用带缓冲的 channel 作为计数信号量：
//   - 获取槽位 = 向 channel 发送（非阻塞）
//   - 释放槽位 = 从 channel 接收
//   - 槽位满 → 立即返回错误（快速失败，不排队）
//
// # 与令牌桶的区别
//
// 令牌桶控制的是"速率"（每秒多少请求），舱壁控制的是
// "并发度"（同时进行中的请求数）。两者互补：
//   - 限流器防止调用过快（频率控制）
//   - 舱壁防止同时太多（并发控制）
package bulkhead

import (
	"context"
	"errors"
)

// ErrBulkheadFull 是舱壁并发槽位已满时返回的错误。
var ErrBulkheadFull = errors.New("bulkhead: concurrency limit reached")

// Bulkhead 使用 channel 作为计数信号量，限制同时进行的操作数。
//
// 与 sync.Mutex 不同：Mutex 只允许 1 个 goroutine 进入，
// Bulkhead 允许配置数量的 goroutine 同时执行——当达到上限时，
// 新增的调用者被立即拒绝，不阻塞排队。
//
// 这种"快速失败"语义对 API 网关很重要：
// 无限排队 → 请求堆积 → 内存耗尽 → OOM。快速失败 → 调用方
// 立刻知道系统繁忙，可以做降级或换一个 endpoint。
type Bulkhead struct {
	sem chan struct{} // 缓冲 channel 作为计数信号量
}

// New 创建一个最多允许 maxConcurrent 个并发操作的舱壁。
// maxConcurrent 至少为 1。
func New(maxConcurrent int) *Bulkhead {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Bulkhead{
		sem: make(chan struct{}, maxConcurrent),
	}
}

// Execute 在舱壁有容量时执行 fn。
//
// 三种返回值：
//   - fn() 的返回值：正常获取槽位并执行
//   - ErrBulkheadFull：槽位已满，fn 未被调用
//   - ctx.Err()：context 在获取槽位之前被取消
//
// 注意：Execute 不阻塞等待——没有槽位立即返回 ErrBulkheadFull。
// 这是刻意设计：在 API 网关场景中，阻塞等待比快速失败危险得多。
func (b *Bulkhead) Execute(ctx context.Context, fn func() error) error {
	// 非阻塞地向信号量发送——如果 channel 满了，走 default 分支。
	select {
	case b.sem <- struct{}{}:
		// 成功获取槽位，函数返回后释放。
		defer func() { <-b.sem }()
		return fn()
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrBulkheadFull
	}
}

// Available 返回当前空闲槽位数。
func (b *Bulkhead) Available() int {
	return cap(b.sem) - len(b.sem)
}

// InUse 返回当前占用的槽位数。
func (b *Bulkhead) InUse() int {
	return len(b.sem)
}

// Capacity 返回最大并发操作数。
func (b *Bulkhead) Capacity() int {
	return cap(b.sem)
}
