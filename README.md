# RAgent Router

**Go 实现的 AI API 智能网关与容错引擎。**

在 Claude Code 与多个大模型供应商之间构建的透明代理层，提供熔断降级、限流控制、Jitter 重试、Token 计量、智能路由与成本分析能力。

## 项目动机

日常使用 Claude Code 开发时遇到了三个痛点：

1. **零容错** — 上游 API 返回 5xx 或被限流时，所有请求直接失败。没有重试、没有降级、没有熔断。
2. **成本不透明** — Token 消耗和 API 费用完全不可见。
3. **路由不智能** — "Redis 是什么"这种一句话问题，和"设计一个分布式任务系统"这种复杂任务，调用的是同一个昂贵模型。

RAgent Router 在 AI 工具和供应商之间加了一层中间件，在不改变客户端使用习惯的前提下解决这三个问题。

## 架构

```
Claude Code / AI 客户端
        │
        │  POST /v1/messages (Anthropic 兼容, SSE 流式)
        │
   ┌────▼──────────────────────────────────────────┐
   │              RAgent Router (Go)                │
   │                                                │
   │  ┌──────────────────────────────────────────┐ │
   │  │         韧性引擎（自研核心）               │ │
   │  │                                          │ │
   │  │  限流器 → 熔断器 → 重试器 → 舱壁 → 超时  │ │
   │  └──────────────────────────────────────────┘ │
   │                     │                          │
   │  ┌──────────────────────────────────────────┐ │
   │  │         路由引擎                          │ │
   │  │  规则匹配 + 关键词 → 选择目标模型         │ │
   │  └──────────────────────────────────────────┘ │
   │                     │                          │
   │  ┌──────────────────────────────────────────┐ │
   │  │         可观测层                          │ │
   │  │  Token 实时解析, 成本估算,               │ │
   │  │  SQLite 持久化, Dashboard API            │ │
   │  └──────────────────────────────────────────┘ │
   └────┬──────────────────────────────────────────┘
        │
   ┌────▼────┐  ┌────▼────┐  ┌────▼────┐
   │  GPT-4  │  │ Claude  │  │DeepSeek │
   └─────────┘  └─────────┘  └─────────┘
```

## 项目结构

```
ragent-router/
├── backend/                          # Go 后端
│   ├── cmd/server/main.go            # HTTP 服务入口
│   ├── internal/
│   │   ├── resilience/               # ★ 核心：韧性引擎
│   │   │   ├── circuitbreaker/       #   三态熔断器 + 滑动窗口
│   │   │   ├── ratelimit/            #   令牌桶 + 分片锁存储
│   │   │   ├── retry/                #   指数退避 + 3 种 Jitter 策略
│   │   │   ├── bulkhead/             #   信号量舱壁隔离
│   │   │   └── timeout/              #   Context 级联超时
│   │   ├── proxy/                    # Anthropic SSE 流式代理
│   │   ├── routing/                  # 关键词规则路由引擎
│   │   └── store/                    # SQLite 持久化 + 分析查询
│   └── demo/demo_test.go             # 8 个集成测试场景
│
├── frontend/                         # React Dashboard (Electron)
│   ├── src/
│   │   ├── api/index.ts              # API 调用层
│   │   ├── pages/Dashboard.tsx       # 仪表盘页面
│   │   ├── pages/TrafficMonitor.tsx  # 流量监控页面
│   │   └── components/               # 图表、表格、设置组件
│   └── package.json
│
└── README.md
```

## 技术亮点

### 1. 熔断器 — 三态状态机 + 滑动窗口

```
Closed   ──(失败率超过阈值)──→ Open
Open     ──(冷却时间到期)───→ HalfOpen
HalfOpen ──(探测请求成功)───→ Closed
HalfOpen ──(探测请求失败)───→ Open
```

- 基于滑动时间窗口统计失败率，避免固定窗口的边界效应
- 每个上游供应商独立熔断，故障隔离互不影响
- 半开状态只允许配置数量的探测请求通过，防止流量冲击刚恢复的服务

### 2. 令牌桶限流 — Lazy Refill + 分片锁

热路径（Token 充足时）只需 **一次 Mutex 加锁 + 一次整数递减**。时间差计算推迟到桶耗尽时才执行，避免每次请求都调用 `time.Now()`。

```
BenchmarkTokenBucket_Allow-32    42440167    28.32 ns/op    0 B/op    0 allocs/op
```

底层存储采用 **FNV-64a 哈希**将 key 分散到 **2048 个分片**，每个分片持有独立的 `sync.RWMutex`。`Load` 路径使用 **Double-Checked Locking** 避免写锁竞争：

```go
// 快速路径：乐观读（无竞争）
sh.mu.RLock()
v, ok := sh.data[key]
sh.mu.RUnlock()
if ok { return v }

// 慢速路径：写锁 + 二次检查（仅在缓存未命中时）
sh.mu.Lock()
v, ok = sh.data[key]  // ← 二次确认，防止 TOCTOU 竞争
if ok { return v }
sh.data[key] = newVal
sh.mu.Unlock()
```

### 3. 重试策略 — Jitter 抖动

| 策略 | 公式 | 适用场景 |
|---|---|---|
| Full Jitter | `random(0, cap)` | 防止惊群效应 |
| Equal Jitter | `cap/2 + random(0, cap/2)` | 兼顾定时精度与分散 |
| Decorrelated Jitter | `min(cap, random(base, cap×3))` | 跨节点独立重试时序 |

### 4. Context 级联超时

子 deadline 受父约束。重试循环中每次 10 秒超时，不会因为没设父 deadline 而实际跑了 50 秒。

```go
rootCtx := timeout.Cascading(ctx, 30*time.Second)   // 总预算 30s
for i := 0; i < 3; i++ {
    attemptCtx := timeout.Cascading(rootCtx, 10*time.Second) // 每次尝试受父约束
    doRequest(attemptCtx)
}
```

### 5. 供应商适配器模式

通过接口抽象屏蔽不同 AI 供应商的协议差异。代理层统一使用 Anthropic Messages API 格式，适配器负责翻译为供应商原生格式：

```go
type ProviderAdapter interface {
    BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (
        url string, reqHeaders map[string]string, reqBody []byte, err error)
}
```

新增供应商只需实现此接口，代理核心代码无需修改。

## 快速开始

### 环境要求

- Go 1.22+
- Node.js 18+

### 后端

```bash
cd backend

# 安装依赖
GOPROXY=https://goproxy.cn,direct go mod download

# 通过环境变量配置供应商后启动
DEEPSEEK_API_KEY=sk-your-key \
CLAUDE_API_KEY=sk-ant-your-key \
go run ./cmd/server

# 或通过 JSON 配置
PROVIDERS='[{"id":"1","name":"DeepSeek","base_url":"https://api.deepseek.com","api_key":"sk-...","model":"deepseek-chat","enabled":true}]' \
go run ./cmd/server
```

服务启动在 `http://localhost:15722`。

### 前端

```bash
cd frontend
npm install
npm start
```

### 测试

```bash
cd backend
go test -v ./demo/          # 8 个集成测试场景
go test -bench=. ./demo/    # 性能基准测试
```

## API 端点

### 代理（Anthropic 兼容）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/v1/messages` | SSE 流式代理（Claude Code 调用此端点） |
| GET | `/v1/messages` | 健康检查 |

### Dashboard

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/dashboard/overview` | 今日/本月费用、总请求数 |
| GET | `/api/dashboard/model-distribution` | 各模型请求分布及占比 |
| GET | `/api/dashboard/recent-routes` | 最近请求日志（可配置条数） |
| GET | `/api/dashboard/cost-trend` | 每日费用 + 请求量趋势 |

### 监控

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/monitor/overview` | 聚合统计（Token、费用、延迟、错误） |
| GET | `/api/monitor/recent` | 原始请求日志 |
| GET | `/api/monitor/by-model` | 各模型延迟 + 费用明细 |

### 供应商管理

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/proxy/current` | 当前活跃供应商 |
| POST | `/api/proxy/activate/{id}` | 切换活跃供应商 |
| GET | `/api/proxy/health` | 系统健康状态 + 告警 |
| GET | `/api/ccswitch/providers` | 已注册供应商列表 |

### 韧性引擎

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/resilience/stats` | 各供应商熔断器状态 |

### 健康检查

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/health` | 服务探活 |

## 路由规则

默认规则（可通过代码配置）：

| 规则 | 关键词 | 目标模型 | 优先级 |
|---|---|---|---|
| 架构与设计 | architecture, design, refactor, 架构, 设计, 重构 | Claude | 100 |
| Bug 修复与调试 | bug, fix, debug, error, 修复, 调试 | Claude | 90 |
| 代码生成 | generate, create, implement, 生成, 创建 | Claude | 80 |
| 复杂分析 | analyze, review (提示词 >300 token) | Claude | 70 |
| 简单问答 | explain, what is, how to, 解释, 什么是 | DeepSeek | 50 |
| 文档 | document, readme, doc, 文档 | DeepSeek | 40 |

未命中任何规则的请求默认路由到 DeepSeek（最具成本效益）。

## 技术栈

| 层级 | 技术选型 |
|---|---|
| 后端语言 | Go 1.22+ |
| HTTP 服务 | `net/http`（标准库，零外部依赖） |
| 数据库 | SQLite（`modernc.org/sqlite`，纯 Go 无 CGO） |
| 前端框架 | React + TypeScript |
| UI 组件 | Ant Design + Recharts |
| 桌面环境 | Electron + Vite |
| 状态管理 | Zustand |

## License

MIT
