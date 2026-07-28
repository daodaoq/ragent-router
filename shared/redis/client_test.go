package redis

import (
	"context"
	"testing"
)

func TestAllow_NilClient(t *testing.T) {
	// Redis 不可用时应降级放行
	old := Client
	Client = nil
	defer func() { Client = old }()

	result := Allow(context.Background(), "test:key", 10, 5.0)
	if !result {
		t.Error("Redis 不可用时应降级放行")
	}
}
