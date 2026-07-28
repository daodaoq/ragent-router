// Package discovery 提供基于 etcd 的服务注册与发现。
//
// 面试考点：
//  1. 服务注册：服务启动时向 etcd 注册自己的地址，设置 TTL 租约
//  2. 服务发现：从 etcd 查询目标服务的可用实例列表
//  3. 健康检查：通过 KeepAlive 续租，服务崩溃后租约自动过期
//  4. 负载均衡：go-zero 内置 P2C 算法，随机选两个取负载低的
package discovery

import (
	"context"
	"fmt"
	"log"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// ServiceRegistry 服务注册器。
type ServiceRegistry struct {
	client    *clientv3.Client
	key       string // 注册键，如 /services/router/192.168.1.10:8082
	value     string // 注册值，如 {"name":"router","addr":"192.168.1.10:8082"}
	leaseID   clientv3.LeaseID
	ttl       int64 // 租约 TTL（秒）
	keepAlive <-chan *clientv3.LeaseKeepAliveResponse
}

// NewServiceRegistry 创建服务注册器。
//
// 参数：
//   - endpoints: etcd 地址列表，如 ["127.0.0.1:2379"]
//   - serviceName: 服务名，如 "router"
//   - addr: 服务地址，如 "192.168.1.10:8082"
//   - ttl: 租约 TTL（秒），默认 10
func NewServiceRegistry(endpoints []string, serviceName, addr string, ttl int64) (*ServiceRegistry, error) {
	if ttl <= 0 {
		ttl = 10
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("etcd 连接失败: %w", err)
	}

	key := fmt.Sprintf("/services/%s/%s", serviceName, addr)
	value := fmt.Sprintf(`{"name":"%s","addr":"%s"}`, serviceName, addr)

	return &ServiceRegistry{
		client: client,
		key:    key,
		value:  value,
		ttl:    ttl,
	}, nil
}

// Register 注册服务到 etcd。
//
// 流程：
//  1. 创建租约（Lease），TTL = 10s
//  2. Put key-value 并绑定租约
//  3. 启动 KeepAlive 自动续租
//  4. 服务崩溃 → KeepAlive 停止 → 租约过期 → key 自动删除
func (r *ServiceRegistry) Register(ctx context.Context) error {
	// 创建租约
	resp, err := r.client.Grant(ctx, r.ttl)
	if err != nil {
		return fmt.Errorf("创建租约失败: %w", err)
	}
	r.leaseID = resp.ID

	// 注册服务（绑定租约）
	_, err = r.client.Put(ctx, r.key, r.value, clientv3.WithLease(r.leaseID))
	if err != nil {
		return fmt.Errorf("注册服务失败: %w", err)
	}

	// 启动自动续租
	r.keepAlive, err = r.client.KeepAlive(ctx, r.leaseID)
	if err != nil {
		return fmt.Errorf("启动续租失败: %w", err)
	}

	// 消费续租响应（防止 channel 满）
	go func() {
		for range r.keepAlive {
			// 续租成功，继续
		}
		log.Printf("[服务注册] 续租停止: %s", r.key)
	}()

	log.Printf("[服务注册] ✓ %s → %s (TTL=%ds)", r.key, r.value, r.ttl)
	return nil
}

// Deregister 注销服务（优雅关闭时调用）。
func (r *ServiceRegistry) Deregister(ctx context.Context) error {
	// 撤销租约 → 自动删除所有绑定的 key
	_, err := r.client.Revoke(ctx, r.leaseID)
	if err != nil {
		return fmt.Errorf("注销服务失败: %w", err)
	}
	log.Printf("[服务注册] ✓ 已注销: %s", r.key)
	return r.client.Close()
}

// ────────────────────────────────────────────────────────────
// 服务发现
// ────────────────────────────────────────────────────────────

// ServiceDiscovery 服务发现器。
type ServiceDiscovery struct {
	client *clientv3.Client
}

// NewServiceDiscovery 创建服务发现器。
func NewServiceDiscovery(endpoints []string) (*ServiceDiscovery, error) {
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("etcd 连接失败: %w", err)
	}
	return &ServiceDiscovery{client: client}, nil
}

// Discover 查询指定服务的所有可用实例。
//
// 前缀查询：/services/router/ → 返回所有 router 实例的地址列表。
func (d *ServiceDiscovery) Discover(ctx context.Context, serviceName string) ([]string, error) {
	prefix := fmt.Sprintf("/services/%s/", serviceName)
	resp, err := d.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("查询服务失败: %w", err)
	}

	var addrs []string
	for _, kv := range resp.Kvs {
		// value 格式: {"name":"router","addr":"192.168.1.10:8082"}
		// 简单提取 addr 字段
		val := string(kv.Value)
		if idx := len(`{"name":"`); len(val) > idx {
			// 从 JSON 中提取 addr（简化处理）
			addrs = append(addrs, extractAddr(val))
		}
	}
	return addrs, nil
}

// Watch 监听服务变更（新增/下线实例时触发回调）。
func (d *ServiceDiscovery) Watch(ctx context.Context, serviceName string, onChange func(addrs []string)) {
	prefix := fmt.Sprintf("/services/%s/", serviceName)
	wch := d.client.Watch(ctx, prefix, clientv3.WithPrefix())

	go func() {
		for resp := range wch {
			_ = resp
			// 实例变更，重新查询
			addrs, err := d.Discover(ctx, serviceName)
			if err != nil {
				log.Printf("[服务发现] 查询失败: %v", err)
				continue
			}
			onChange(addrs)
		}
	}()
}

func extractAddr(json string) string {
	// 简化提取：找 "addr":"..." 中的值
	start := 0
	for i := 0; i < len(json)-7; i++ {
		if json[i:i+7] == `"addr":"` {
			start = i + 7
			break
		}
	}
	for i := start; i < len(json); i++ {
		if json[i] == '"' {
			return json[start:i]
		}
	}
	return ""
}
