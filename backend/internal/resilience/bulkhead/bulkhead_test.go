package bulkhead

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// 基本功能测试
// =============================================================================

func TestBulkhead_Basic(t *testing.T) {
	b := New(3)

	if b.Capacity() != 3 {
		t.Errorf("Capacity: want=3, got=%d", b.Capacity())
	}
	if b.InUse() != 0 {
		t.Errorf("InUse: want=0, got=%d", b.InUse())
	}
	if b.Available() != 3 {
		t.Errorf("Available: want=3, got=%d", b.Available())
	}

	// 执行一个函数
	executed := false
	err := b.Execute(context.Background(), func() error {
		executed = true
		return nil
	})
	if err != nil {
		t.Fatalf("Execute 错误: %v", err)
	}
	if !executed {
		t.Fatal("函数未被执行")
	}
}

func TestBulkhead_Full(t *testing.T) {
	b := New(2)

	// 占满 2 个槽位
	started := make(chan struct{})
	block := make(chan struct{})

	for i := 0; i < 2; i++ {
		go func() {
			b.Execute(context.Background(), func() error {
				started <- struct{}{}
				<-block
				return nil
			})
		}()
	}

	// 等待两个 goroutine 获取槽位
	<-started
	<-started

	// 第 3 个应被拒绝
	err := b.Execute(context.Background(), func() error {
		t.Fatal("满时不应执行")
		return nil
	})
	if err != ErrBulkheadFull {
		t.Fatalf("舱壁满应返回 ErrBulkheadFull, got=%v", err)
	}

	// 释放槽位
	close(block)
	time.Sleep(10 * time.Millisecond)

	// 现在应能执行
	executed := false
	err = b.Execute(context.Background(), func() error {
		executed = true
		return nil
	})
	if err != nil {
		t.Fatalf("释放后 Execute 错误: %v", err)
	}
	if !executed {
		t.Fatal("释放后函数未被执行")
	}
}

func TestBulkhead_ContextCancel(t *testing.T) {
	b := New(1)

	// 占满槽位
	started := make(chan struct{})
	block := make(chan struct{})
	go func() {
		b.Execute(context.Background(), func() error {
			started <- struct{}{}
			<-block
			return nil
		})
	}()
	<-started

	// 用已取消的 context 尝试执行
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := b.Execute(ctx, func() error {
		return nil
	})
	if err != context.Canceled {
		t.Fatalf("已取消 context 应返回 context.Canceled, got=%v", err)
	}

	close(block)
}

func TestBulkhead_FunctionError(t *testing.T) {
	b := New(3)
	expectedErr := errors.New("function error")

	err := b.Execute(context.Background(), func() error {
		return expectedErr
	})
	if err != expectedErr {
		t.Fatalf("函数错误应透传, want=%v, got=%v", expectedErr, err)
	}
}

func TestBulkhead_MinCapacity(t *testing.T) {
	// capacity < 1 应被修正为 1
	b := New(0)
	if b.Capacity() != 1 {
		t.Errorf("capacity=0 应被修正为 1, got=%d", b.Capacity())
	}

	b2 := New(-5)
	if b2.Capacity() != 1 {
		t.Errorf("capacity=-5 应被修正为 1, got=%d", b2.Capacity())
	}
}

// =============================================================================
// 并发安全测试
// =============================================================================

func TestBulkhead_Concurrent(t *testing.T) {
	b := New(10)

	const goroutines = 100
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	var successCount, rejectCount atomic.Int64

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				err := b.Execute(context.Background(), func() error {
					// 模拟短暂工作
					time.Sleep(time.Microsecond)
					return nil
				})
				if err == ErrBulkheadFull {
					rejectCount.Add(1)
				} else if err == nil {
					successCount.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	t.Logf("并发测试: success=%d, rejected=%d", successCount.Load(), rejectCount.Load())
	if successCount.Load() == 0 {
		t.Fatal("没有请求被成功执行")
	}
}

func TestBulkhead_ConcurrentCapacityLimit(t *testing.T) {
	// 验证并发执行数永远不会超过 Capacity
	const capacity = 5
	b := New(capacity)

	var currentConcurrent atomic.Int32
	var maxConcurrent atomic.Int32

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				b.Execute(context.Background(), func() error {
					current := currentConcurrent.Add(1)
					// 更新最大并发数
					for {
						old := maxConcurrent.Load()
						if current <= old || maxConcurrent.CompareAndSwap(old, current) {
							break
						}
					}
					time.Sleep(time.Millisecond)
					currentConcurrent.Add(-1)
					return nil
				})
			}
		}()
	}

	wg.Wait()

	if maxConcurrent.Load() > int32(capacity) {
		t.Errorf("最大并发数超过 Capacity: max=%d, capacity=%d", maxConcurrent.Load(), capacity)
	}
	t.Logf("最大并发数: %d (capacity=%d)", maxConcurrent.Load(), capacity)
}

