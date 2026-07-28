package timeout

import (
	"context"
	"testing"
	"time"
)

func TestCascading_ParentTighter(t *testing.T) {
	// 父 context deadline = 5s，子 timeout = 10s
	// 应使用父的 deadline（更紧）
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	child, childCancel := Cascading(parent, 10*time.Second)
	defer childCancel()

	deadline, ok := child.Deadline()
	if !ok {
		t.Fatal("子 context 应有 deadline")
	}

	remaining := time.Until(deadline)
	// 应接近 5s（父的 deadline），而不是 10s
	if remaining > 6*time.Second {
		t.Errorf("子 deadline 应受父约束: remaining=%v (期望 ~5s)", remaining)
	}
}

func TestCascading_ChildTighter(t *testing.T) {
	// 父 context deadline = 10s，子 timeout = 3s
	// 应使用子的 timeout（更紧）
	parent, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	child, childCancel := Cascading(parent, 3*time.Second)
	defer childCancel()

	deadline, ok := child.Deadline()
	if !ok {
		t.Fatal("子 context 应有 deadline")
	}

	remaining := time.Until(deadline)
	// 应接近 3s（子的 timeout）
	if remaining > 4*time.Second || remaining < 2*time.Second {
		t.Errorf("子 deadline 应为 ~3s: remaining=%v", remaining)
	}
}

func TestCascading_NoParentDeadline(t *testing.T) {
	// 父 context 无 deadline，子 timeout = 5s
	child, cancel := Cascading(context.Background(), 5*time.Second)
	defer cancel()

	deadline, ok := child.Deadline()
	if !ok {
		t.Fatal("子 context 应有 deadline")
	}

	remaining := time.Until(deadline)
	if remaining > 6*time.Second || remaining < 4*time.Second {
		t.Errorf("子 deadline 应为 ~5s: remaining=%v", remaining)
	}
}

func TestRemaining_WithDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	remaining := Remaining(ctx)
	if remaining > 6*time.Second || remaining < 4*time.Second {
		t.Errorf("Remaining: want ~5s, got=%v", remaining)
	}
}

func TestRemaining_NoDeadline(t *testing.T) {
	remaining := Remaining(context.Background())
	if remaining != 0 {
		t.Errorf("无 deadline 应返回 0, got=%v", remaining)
	}
}

func TestRemaining_Expired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), -1*time.Second)
	defer cancel()

	remaining := Remaining(ctx)
	if remaining != 0 {
		t.Errorf("已过期应返回 0, got=%v", remaining)
	}
}

func TestWithBudget(t *testing.T) {
	ctx, cancel := WithBudget(context.Background(), 3*time.Second)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("WithBudget 应创建 deadline")
	}

	remaining := time.Until(deadline)
	if remaining > 4*time.Second || remaining < 2*time.Second {
		t.Errorf("WithBudget deadline 应为 ~3s: remaining=%v", remaining)
	}
}
